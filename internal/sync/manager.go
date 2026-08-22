// Package sync handles synchronization between PocketBase and NATS
package sync

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	"github.com/skeeeon/pb-nats/internal/publisher"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
	"github.com/skeeeon/pb-nats/internal/utils"
)

// Manager orchestrates real-time synchronization between PocketBase record changes and NATS.
type Manager struct {
	app             *pocketbase.PocketBase
	jwtGen          *jwt.Generator
	nkeyManager     *nkey.Manager
	publisher       *publisher.Manager
	options         pbtypes.Options
	logger          *utils.Logger
	systemAccountID string

	// Debouncing state
	timer      *time.Timer
	timerMutex sync.Mutex
}

// NewManager creates a new sync manager with all required dependencies.
func NewManager(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager,
	publisher *publisher.Manager, options pbtypes.Options, systemAccountID string) *Manager {
	return &Manager{
		app:             app,
		jwtGen:          jwtGen,
		nkeyManager:     nkeyManager,
		publisher:       publisher,
		options:         options,
		systemAccountID: systemAccountID,
		logger:          utils.NewLogger(options.LogToConsole),
	}
}

// ReconcileAccounts enqueues a publish for every active account, converging the NATS
// server's view onto PocketBase's. Account JWTs live only in the NATS resolver
// directory, so without this a lost or rebuilt resolver leaves every tenant absent
// from NATS until someone happens to edit each record.
//
// This leans entirely on the existing publish queue: operations dedupe per account,
// retry, and wait out bootstrap mode, so a run while NATS is down costs nothing but a
// queue row. Republishing is cheap on the server too — NATS answers "jwt update
// skipped" when it already holds an equal-or-newer JWT.
func (sm *Manager) ReconcileAccounts() error {
	records, err := sm.app.FindAllRecords(sm.options.AccountCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to load accounts for reconciliation")
	}

	queued := 0
	for _, record := range records {
		if record.Id == sm.systemAccountID {
			continue
		}
		// Inactive accounts are absent from NATS by design.
		if !record.GetBool("active") {
			continue
		}
		if err := sm.publisher.QueueAccountUpdate(record.Id, pbtypes.PublishActionUpsert, "", ""); err != nil {
			sm.logger.Warning("Failed to queue account %s for reconciliation: %v", record.Id, err)
			continue
		}
		queued++
	}

	if queued > 0 {
		sm.logger.Info("Queued %d account(s) for reconciliation with NATS", queued)
	}
	return nil
}

// SetupHooks registers PocketBase event hooks for real-time NATS synchronization.
func (sm *Manager) SetupHooks() error {
	sm.logger.Info("Setting up PocketBase hooks for NATS sync...")

	sm.setupAccountHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()
	sm.setupExportHooks()
	sm.setupImportHooks()
	sm.setupProtectionHooks()

	sm.logger.Success("PocketBase hooks configured for NATS sync")
	return nil
}

// setupProtectionHooks refuses deletions pb-nats cannot recover from.
//
// Request-scoped on purpose: these guard the admin UI and the REST API, not a
// deliberate server-side app.Delete() by the consuming application, which is
// assumed to know what it is doing. All of these collections are locked by
// default, so the caller is a superuser either way — which is exactly why the
// clicks worth refusing are the ones that look routine.
func (sm *Manager) setupProtectionHooks() {
	sm.app.OnRecordDeleteRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if reason := sm.protectedDeleteReason(e.Collection.Name, e.Record); reason != "" {
			return utils.WrapError(fmt.Errorf("%s", reason), "record deletion refused")
		}
		return e.Next()
	})
}

// protectedDeleteReason reports why a record must not be deleted, or "" when the
// delete is allowed.
func (sm *Manager) protectedDeleteReason(collectionName string, record *core.Record) string {
	switch collectionName {
	case pbtypes.SystemOperatorCollectionName:
		// The operator seed is the root of trust and lives nowhere else. pb-nats has
		// no keystore and no key export, so deleting this row orphans every account
		// JWT and every distributed creds file at once, with no repair short of
		// restoring the database from backup.
		return "cannot delete the system operator — it is the root of trust for every account and user JWT, and its seed exists nowhere outside this record"

	case sm.options.AccountCollectionName:
		if record.Id == sm.systemAccountID {
			return "cannot delete the system account — it is the account pb-nats itself connects through"
		}

	case sm.options.UserCollectionName:
		if record.GetString("nats_username") == "sys" && record.GetString("account_id") == sm.systemAccountID {
			return "cannot delete the system user — it authenticates pb-nats' own NATS connection, and JWT synchronization stops until the process is restarted"
		}

	case sm.options.RoleCollectionName:
		// role_id is a required relation with CascadeDelete: false, so deleting a
		// role still in use leaves its users pointing at a row that is gone. Nothing
		// notices at delete time; the next JWT regeneration then fails for every one
		// of those users, and a user whose role cannot be resolved can no longer be
		// reissued at all.
		users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": record.Id})
		if err != nil {
			// Can't establish the reference count; let the delete through rather than
			// blocking legitimate cleanup on a query error.
			return ""
		}
		if len(users) > 0 {
			return fmt.Sprintf("cannot delete role %q — %d user(s) still reference it; reassign them to another role first",
				record.GetString("name"), len(users))
		}
	}

	return ""
}

// setupAccountHooks registers hooks for account lifecycle and management operations.
func (sm *Manager) setupAccountHooks() {
	// Account creation - fires for ALL creates (API and programmatic)
	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}
		if e.Record.Id == sm.systemAccountID {
			return e.Next()
		}

		if err := sm.generateAccountKeys(e.Record); err != nil {
			sm.logger.Warning("Failed to generate account keys for %s: %v", e.Record.Id, err)
			return e.Next()
		}

		if e.Record.GetString("public_key") != "" {
			if err := sm.app.Save(e.Record); err != nil {
				sm.logger.Warning("Failed to save account keys for %s: %v", e.Record.Id, err)
				return e.Next()
			}
		}

		// An account created inactive is never published — it has no presence in
		// NATS until it is activated.
		if e.Record.GetBool("active") && sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountCreate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Account updates.
	//
	// Bound to OnRecordUpdate, the model-level hook, NOT OnRecordUpdateRequest.
	// The trigger fields below are the account's whole key-management API, and a
	// request-scoped hook only fires for updates arriving over the REST API — a
	// server-side `app.Save()` from a custom route, a CLI command, a migration or
	// another hook would persist `rotate_keys: true` and silently do nothing,
	// leaving the flag set to fire later on an unrelated update. Model-level
	// covers both paths.
	//
	// Safe against recursion: the helpers called here mutate the in-memory record
	// only and rely on the caller's save to persist, so none of them re-enter
	// app.Save.
	sm.app.OnRecordUpdate().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}

		if e.Record.GetBool("rotate_keys") {
			e.Record.Set("rotate_keys", false)
			if err := sm.rotateAccountSigningKeys(e.Record); err != nil {
				return utils.WrapError(err, "failed to rotate account signing keys")
			}
			sm.logger.Info("Signing keys rotated for account %s - all user JWTs in this account are now invalid",
				e.Record.GetString("name"))
		} else if e.Record.GetBool("add_signing_key") {
			e.Record.Set("add_signing_key", false)
			if err := sm.addAccountSigningKey(e.Record); err != nil {
				return utils.WrapError(err, "failed to add account signing key")
			}
			sm.logger.Info("New signing key added to account %s", e.Record.GetString("name"))
		} else if removeKey := e.Record.GetString("remove_signing_key"); removeKey != "" {
			e.Record.Set("remove_signing_key", "")
			if err := sm.removeAccountSigningKey(e.Record, removeKey); err != nil {
				return utils.WrapError(err, "failed to remove account signing key")
			}
			sm.logger.Info("Signing key removed from account %s", e.Record.GetString("name"))
		} else {
			if err := sm.generateAccountJWT(e.Record); err != nil {
				return utils.WrapError(err, "failed to regenerate account JWT")
			}
		}
		return e.Next()
	})

	// The `active` flag is the account's durable presence in NATS, and it is
	// edge-triggered: only a change in the flag moves the account in or out of the
	// server. Withdrawing an account (via $SYS.REQ.CLAIMS.DELETE) makes the NATS
	// server zero the account's connection/subscription limits, disconnect its
	// clients and disable its JetStream — and republishing the JWT restores all of
	// it, which is what makes deactivation a reversible suspend rather than a
	// destructive delete.
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}
		if e.Record.Id == sm.systemAccountID {
			return e.Next()
		}
		if !sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountUpdate) {
			return e.Next()
		}

		// A signing key that disappeared takes every user JWT it signed with it: NATS
		// looks the JWT's issuer up in the account's signing keys and rejects it
		// outright when absent, independent of the revocation list. Reissue the
		// account's users so they aren't left holding dead credentials.
		//
		// This runs here, after the account row is committed, because user JWTs are
		// signed with the account's stored key set — regenerating them from inside the
		// update hook would sign them with the keys we just replaced. Adding a key
		// removes nothing and so does not trigger this.
		if signingKeysRemoved(e.Record) {
			if err := sm.regenerateUsersInAccount(e.Record.Id); err != nil {
				sm.logger.Warning("Failed to reissue user JWTs for account %s: %v", e.Record.Id, err)
			}
		}

		wasActive := hasPriorState(e.Record) && e.Record.Original().GetBool("active")
		isActive := e.Record.GetBool("active")

		switch {
		case wasActive && !isActive:
			sm.scheduleDelete(e.Record)
			sm.logger.Info("Account %s deactivated — withdrawing from NATS, connected clients will be disconnected",
				e.Record.GetString("name"))
		case isActive:
			// Covers reactivation (the JWT was regenerated by the update hook) and
			// every ordinary update to an active account.
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		default:
			// Already withdrawn from NATS; nothing to publish.
		}
		return e.Next()
	})

	// Account deletion. Refusing to delete the system account is handled by
	// setupProtectionHooks along with the other unrecoverable deletes; NATS removal
	// is scheduled after the delete succeeds using snapshot data from the record
	// (the account row no longer exists when the queue is processed).
	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}
		if e.Record.Id == sm.systemAccountID {
			return e.Next()
		}
		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountDelete) {
			sm.scheduleDelete(e.Record)
		}
		return e.Next()
	})
}

// setupUserHooks registers hooks for user lifecycle and JWT management operations.
func (sm *Manager) setupUserHooks() {
	// User creation
	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if err := sm.generateUserKeys(e.Record); err != nil {
			sm.logger.Warning("Failed to generate user keys for %s: %v", e.Record.Id, err)
			return e.Next()
		}

		if e.Record.GetString("public_key") != "" {
			if err := sm.app.Save(e.Record); err != nil {
				sm.logger.Warning("Failed to save user keys for %s: %v", e.Record.Id, err)
				return e.Next()
			}
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserCreate) {
			accountID := e.Record.GetString("account_id")
			if accountID != "" {
				sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User updates - only regenerate the JWT when a field embedded in it changed
	// (or the regenerate flag is set), so unrelated edits don't rotate credentials.
	//
	// Model-level, for the same reason as the account hook above: `regenerate` and
	// `revoke` are the credential API, and they have to work for a server-side
	// app.Save() as well as a REST update. A consumer's self-service rotation route
	// that sets `regenerate` and saves would otherwise be a silent no-op.
	sm.app.OnRecordUpdate().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// `revoke` is the imperative "these credentials leaked" button: it kills the
		// credentials currently in the wild and immediately issues replacements. The
		// user stays active. Because the leaked material includes the user's seed,
		// this rotates the whole key pair rather than reissuing a JWT for the same
		// key — which also sidesteps the revocation timestamp race, since the new
		// JWT is bound to a public key that no revocation covers.
		if e.Record.GetBool("revoke") {
			e.Record.Set("revoke", false)
			if err := sm.rotateUserCredentials(e.Record); err != nil {
				return utils.WrapError(err, "failed to revoke user credentials")
			}
			sm.logger.Info("Credentials rotated for user %s — the previous creds file is now rejected by NATS",
				e.Record.GetString("nats_username"))
			return e.Next()
		}

		// `active` is the durable suspend switch, edge-triggered so that unrelated
		// saves never churn a user's credentials.
		if hasPriorState(e.Record) {
			wasActive := e.Record.Original().GetBool("active")
			isActive := e.Record.GetBool("active")
			switch {
			case wasActive && !isActive:
				// Suspend: revoke, and deliberately do not reissue. The user has no
				// working credentials until reactivated.
				if err := sm.revokeUserKey(e.Record); err != nil {
					return utils.WrapError(err, "failed to suspend user")
				}
				sm.logger.Info("User %s deactivated — credentials revoked until reactivated",
					e.Record.GetString("nats_username"))
				return e.Next()
			case !wasActive && isActive:
				// Reactivate: a freshly issued JWT carries a later issue time than the
				// revocation cutoff, so NATS accepts it while the old creds stay dead.
				if err := sm.regenerateUserJWT(e.Record); err != nil {
					return utils.WrapError(err, "failed to reactivate user")
				}
				sm.logger.Info("User %s reactivated — new credentials issued",
					e.Record.GetString("nats_username"))
				return e.Next()
			}
		}

		regenerate := e.Record.GetBool("regenerate")
		if regenerate {
			e.Record.Set("regenerate", false)
		}

		if regenerate || e.Record.GetString("jwt") == "" || userJWTFieldsChanged(e.Record) {
			if err := sm.regenerateUserJWT(e.Record); err != nil {
				return utils.WrapError(err, "failed to regenerate user JWT")
			}
			if regenerate {
				sm.logger.Info("JWT regenerated for user %s due to regenerate flag", e.Record.GetString("nats_username"))
			}
		}
		return e.Next()
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// Keep the persistent NATS connection authenticated when the system
		// user's credentials change.
		sm.refreshSystemUserCredentials(e.Record)

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserUpdate) {
			accountID := e.Record.GetString("account_id")
			if accountID != "" {
				sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User deletion. A user JWT is a bearer credential the client keeps (it is not
	// pushed to NATS), so deleting the record alone would leave those credentials
	// working. Revoke the deleted user's key on the account so NATS rejects them,
	// then republish the account.
	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserDelete) {
			accountID := e.Record.GetString("account_id")
			pubKey := e.Record.GetString("public_key")
			if accountID != "" {
				if pubKey != "" {
					if err := sm.revokeAccountUserKey(accountID, pubKey, time.Now()); err != nil {
						sm.logger.Warning("Failed to revoke deleted user %s on account %s: %v",
							utils.TruncateString(pubKey, 20), accountID, err)
						sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
					}
				} else {
					sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
				}
			}
		}
		return e.Next()
	})
}

// setupRoleHooks registers hooks for role permission changes affecting multiple users.
func (sm *Manager) setupRoleHooks() {
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.RoleCollectionName, pbtypes.EventTypeRoleUpdate) {
			if err := sm.regenerateUsersWithRole(e.Record.Id); err != nil {
				sm.logger.Warning("Failed to regenerate users with role %s: %v", e.Record.Id, err)
			}
			if err := sm.scheduleAccountsWithRole(e.Record.Id); err != nil {
				sm.logger.Warning("Failed to schedule accounts with role %s: %v", e.Record.Id, err)
			}
		}
		return e.Next()
	})
}

// setupExportHooks registers hooks for account export changes.
// When exports change, the owning account's JWT is regenerated and published.
func (sm *Manager) setupExportHooks() {
	regenerateAccountFromRecord := func(e *core.RecordEvent) error {
		accountID := e.Record.GetString("account_id")
		if accountID == "" || accountID == sm.systemAccountID {
			return e.Next()
		}
		sm.regenerateAndSyncAccount(accountID)
		return e.Next()
	}

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ExportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ExportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})

	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ExportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})
}

// setupImportHooks registers hooks for account import changes.
// When imports change, the owning account's JWT is regenerated and published.
func (sm *Manager) setupImportHooks() {
	regenerateAccountFromRecord := func(e *core.RecordEvent) error {
		accountID := e.Record.GetString("account_id")
		if accountID == "" || accountID == sm.systemAccountID {
			return e.Next()
		}
		sm.regenerateAndSyncAccount(accountID)
		return e.Next()
	}

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ImportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ImportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})

	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.ImportCollectionName {
			return e.Next()
		}
		return regenerateAccountFromRecord(e)
	})
}

// regenerateAndSyncAccount regenerates an account's JWT and schedules it for NATS publishing.
func (sm *Manager) regenerateAndSyncAccount(accountID string) {
	accountRecord, err := sm.app.FindRecordById(sm.options.AccountCollectionName, accountID)
	if err != nil {
		sm.logger.Warning("Failed to find account %s for JWT regeneration: %v", accountID, err)
		return
	}
	if err := sm.generateAccountJWT(accountRecord); err != nil {
		sm.logger.Warning("Failed to regenerate account JWT for %s: %v", accountID, err)
		return
	}
	if err := sm.app.Save(accountRecord); err != nil {
		sm.logger.Warning("Failed to save account %s after JWT regeneration: %v", accountID, err)
		return
	}
	sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
}

// revokeUserKey revokes a user's current public key on the owning account, so NATS
// rejects any credentials the user still holds. It does not touch the user's active
// state — callers own that.
//
// The stored JWT/creds are intentionally left in place — they are already rejected
// by NATS and clearing the JWT would cause the next unrelated save to auto-reissue.
func (sm *Manager) revokeUserKey(record *core.Record) error {
	accountID := record.GetString("account_id")
	pubKey := record.GetString("public_key")
	if accountID == "" || pubKey == "" {
		return fmt.Errorf("user has no account or public key to revoke")
	}

	return sm.revokeAccountUserKey(accountID, pubKey, time.Now())
}

// rotateUserCredentials replaces a user's key pair, revokes the old public key on
// the owning account, and issues a JWT and creds file for the new key. Used when the
// existing credentials are considered compromised: the leaked seed is retired along
// with the JWT.
//
// The old key is revoked and the new JWT is signed for a different public key, so
// the new credentials cannot collide with the revocation cutoff — unlike reissuing
// for the same key, where a cutoff and an issue time landing in the same second
// would leave the fresh JWT revoked as well.
func (sm *Manager) rotateUserCredentials(record *core.Record) error {
	accountID := record.GetString("account_id")
	oldPubKey := record.GetString("public_key")
	if accountID == "" || oldPubKey == "" {
		return fmt.Errorf("user has no account or public key to rotate")
	}

	seed, public, err := sm.nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate replacement user key pair")
	}
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get replacement private key from seed")
	}

	record.Set("public_key", public)
	if err := pbtypes.EncryptAndSet(record, "private_key", privateKey, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt replacement user private key")
	}
	if err := pbtypes.EncryptAndSet(record, "seed", seed, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt replacement user seed")
	}

	if err := sm.revokeAccountUserKey(accountID, oldPubKey, time.Now()); err != nil {
		return utils.WrapError(err, "failed to revoke previous user key")
	}

	return sm.regenerateUserJWT(record)
}

// revokeAccountUserKey adds a revocation for pubKey to the account, regenerates the
// account JWT so the revocation is embedded, persists it, and schedules a NATS
// resync. The cutoff advances but is never removed: user JWTs issued at or before
// it are rejected, while JWTs issued afterward remain valid.
func (sm *Manager) revokeAccountUserKey(accountID, pubKey string, ts time.Time) error {
	accountRecord, err := sm.app.FindRecordById(sm.options.AccountCollectionName, accountID)
	if err != nil {
		return utils.WrapErrorf(err, "failed to find account %s for revocation", accountID)
	}

	account := pbtypes.RecordToAccountModel(accountRecord, sm.options.EncryptionKey)
	updated, err := pbtypes.RevokeInJSON(account.Revocations, pubKey, ts.Unix())
	if err != nil {
		return utils.WrapError(err, "failed to update revocation list")
	}
	accountRecord.Set("revocations", updated)

	if err := sm.generateAccountJWT(accountRecord); err != nil {
		return utils.WrapError(err, "failed to regenerate account JWT after revocation")
	}
	if err := sm.app.Save(accountRecord); err != nil {
		return utils.WrapError(err, "failed to save account after revocation")
	}

	sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
	return nil
}

// rotateAccountSigningKeys performs emergency signing key rotation for an account.
func (sm *Manager) rotateAccountSigningKeys(record *core.Record) error {
	sm.logger.Info("Rotating signing keys for account %s...", record.GetString("name"))

	_, _, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate new account signing key pair")
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return utils.WrapError(err, "failed to get signing private key from seed")
	}

	// Emergency rotation: replace entire array with single new key
	pub, priv := pbtypes.NewSigningKeyPair(signingPublic, signingPrivateKey, signingKey)
	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{pub},
		[]pbtypes.SigningKeyPrivate{priv},
	)
	if err != nil {
		return utils.WrapError(err, "failed to marshal signing keys")
	}

	record.Set("signing_keys", pubJSON)
	if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt signing keys")
	}

	// Every pre-rotation user JWT was signed by a key that no longer exists, so NATS
	// rejects it on the issuer check alone. The revocation entries covering those
	// JWTs can never matter again — drop them and keep the account JWT small.
	record.Set("revocations", json.RawMessage("{}"))

	if err := sm.generateAccountJWT(record); err != nil {
		return utils.WrapError(err, "failed to regenerate account JWT with new signing keys")
	}

	sm.logger.Success("Account signing keys rotated successfully for %s", record.GetString("name"))
	sm.logger.Info("   New signing key: %s", utils.TruncateString(signingPublic, 20))
	sm.logger.Warning("   All previous signing keys removed — user JWTs signed with old keys are now invalid")

	return nil
}

// addAccountSigningKey generates a new signing key and appends it to the account's key array.
// The new key becomes the latest (used for signing new user JWTs). Existing keys remain valid.
func (sm *Manager) addAccountSigningKey(record *core.Record) error {
	_, _, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate new account signing key pair")
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return utils.WrapError(err, "failed to get signing private key from seed")
	}

	// Parse existing keys
	account := pbtypes.RecordToAccountModel(record, sm.options.EncryptionKey)

	newPub, newPriv := pbtypes.NewSigningKeyPair(signingPublic, signingPrivateKey, signingKey)
	pubKeys := append(account.SigningKeys, newPub)
	privKeys := append(account.SigningKeysPrivate, newPriv)

	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(pubKeys, privKeys)
	if err != nil {
		return utils.WrapError(err, "failed to marshal signing keys")
	}

	record.Set("signing_keys", pubJSON)
	if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt signing keys")
	}

	if err := sm.generateAccountJWT(record); err != nil {
		return utils.WrapError(err, "failed to regenerate account JWT")
	}

	sm.logger.Info("   New signing key: %s (now %d total keys)", utils.TruncateString(signingPublic, 20), len(pubKeys))

	return nil
}

// removeAccountSigningKey removes a specific signing key from the account's key array.
// Fails if the key is the only one remaining.
func (sm *Manager) removeAccountSigningKey(record *core.Record, publicKeyToRemove string) error {
	account := pbtypes.RecordToAccountModel(record, sm.options.EncryptionKey)

	if len(account.SigningKeysPrivate) <= 1 {
		return fmt.Errorf("cannot remove the only signing key — use rotate_keys for emergency replacement")
	}

	// Filter out the key to remove
	var newPub []pbtypes.SigningKeyPublic
	var newPriv []pbtypes.SigningKeyPrivate
	found := false

	for _, k := range account.SigningKeys {
		if k.PublicKey != publicKeyToRemove {
			newPub = append(newPub, k)
		} else {
			found = true
		}
	}
	for _, k := range account.SigningKeysPrivate {
		if k.PublicKey != publicKeyToRemove {
			newPriv = append(newPriv, k)
		}
	}

	if !found {
		return fmt.Errorf("signing key %s not found on this account", utils.TruncateString(publicKeyToRemove, 20))
	}

	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(newPub, newPriv)
	if err != nil {
		return utils.WrapError(err, "failed to marshal signing keys")
	}

	record.Set("signing_keys", pubJSON)
	if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt signing keys")
	}

	if err := sm.generateAccountJWT(record); err != nil {
		return utils.WrapError(err, "failed to regenerate account JWT")
	}

	sm.logger.Info("   Removed signing key: %s (now %d total keys)", utils.TruncateString(publicKeyToRemove, 20), len(newPub))
	sm.logger.Warning("   User JWTs signed with the removed key are now invalid")

	return nil
}

// shouldHandleEvent determines if an event should be processed based on configured filters.
func (sm *Manager) shouldHandleEvent(collectionName, eventType string) bool {
	if sm.options.EventFilter != nil {
		return sm.options.EventFilter(collectionName, eventType)
	}
	return true
}

// scheduleSync schedules a NATS publishing operation with debouncing.
func (sm *Manager) scheduleSync(accountID, action string) {
	sm.enqueue(accountID, action, "", "")
}

// scheduleDelete schedules NATS account removal, snapshotting the public key and
// name from the (already deleted) record so the operation can be published later.
func (sm *Manager) scheduleDelete(record *core.Record) {
	sm.enqueue(record.Id, pbtypes.PublishActionDelete, record.GetString("public_key"), record.GetString("name"))
}

// enqueue queues an account operation and (re)arms the debounce timer.
func (sm *Manager) enqueue(accountID, action, publicKey, accountName string) {
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	if sm.timer != nil {
		sm.timer.Stop()
	}

	if err := sm.publisher.QueueAccountUpdate(accountID, action, publicKey, accountName); err != nil {
		sm.logger.Warning("Failed to queue account update for account %s: %v", accountID, err)
	}

	sm.timer = time.AfterFunc(sm.options.DebounceInterval, func() {
		if err := sm.publisher.ProcessPublishQueue(); err != nil {
			sm.logger.Warning("Error processing publish queue: %v", err)
		}
	})
}

// generateAccountKeys generates key pairs and JWT for a new account record.
func (sm *Manager) generateAccountKeys(record *core.Record) error {
	if record.GetString("public_key") != "" {
		return nil
	}

	seed, public, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate account key pair")
	}

	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return utils.WrapError(err, "failed to get signing private key from seed")
	}

	record.Set("public_key", public)
	if err := pbtypes.EncryptAndSet(record, "private_key", privateKey, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt account private key")
	}
	if err := pbtypes.EncryptAndSet(record, "seed", seed, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt account seed")
	}

	pub, priv := pbtypes.NewSigningKeyPair(signingPublic, signingPrivateKey, signingKey)
	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{pub},
		[]pbtypes.SigningKeyPrivate{priv},
	)
	if err != nil {
		return utils.WrapError(err, "failed to marshal signing keys")
	}
	record.Set("signing_keys", pubJSON)
	if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt account signing keys")
	}

	return sm.generateAccountJWT(record)
}

// generateAccountJWT creates an account JWT using the system operator's signing key.
// Fetches exports and imports for the account to embed in the JWT.
func (sm *Manager) generateAccountJWT(record *core.Record) error {
	operator, err := sm.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator")
	}

	account := pbtypes.RecordToAccountModel(record, sm.options.EncryptionKey)

	latestKey := operator.LatestSigningKey()
	if latestKey == nil {
		return utils.WrapError(fmt.Errorf("operator has no signing keys"), "invalid system operator")
	}

	exports := sm.getAccountExports(record.Id)
	imports := sm.getAccountImports(record.Id)

	jwtValue, err := sm.jwtGen.GenerateAccountJWT(account, latestKey.Seed, exports, imports)
	if err != nil {
		return utils.WrapError(err, "failed to generate account JWT")
	}

	record.Set("jwt", jwtValue)
	return nil
}

// getAccountExports fetches all export records for an account.
func (sm *Manager) getAccountExports(accountID string) []*pbtypes.AccountExportRecord {
	records, err := sm.app.FindAllRecords(sm.options.ExportCollectionName, dbx.HashExp{"account_id": accountID})
	if err != nil {
		return nil
	}
	exports := make([]*pbtypes.AccountExportRecord, len(records))
	for i, r := range records {
		exports[i] = pbtypes.RecordToExportModel(r)
	}
	return exports
}

// getAccountImports fetches all import records for an account.
func (sm *Manager) getAccountImports(accountID string) []*pbtypes.AccountImportRecord {
	records, err := sm.app.FindAllRecords(sm.options.ImportCollectionName, dbx.HashExp{"account_id": accountID})
	if err != nil {
		return nil
	}
	imports := make([]*pbtypes.AccountImportRecord, len(records))
	for i, r := range records {
		imports[i] = pbtypes.RecordToImportModel(r)
	}
	return imports
}

// generateUserKeys generates key pairs and JWT for a new user record.
func (sm *Manager) generateUserKeys(record *core.Record) error {
	if record.GetString("public_key") != "" {
		return nil
	}

	seed, public, err := sm.nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate user key pair")
	}

	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
	}

	record.Set("public_key", public)
	if err := pbtypes.EncryptAndSet(record, "private_key", privateKey, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt user private key")
	}
	if err := pbtypes.EncryptAndSet(record, "seed", seed, sm.options.EncryptionKey); err != nil {
		return utils.WrapError(err, "failed to encrypt user seed")
	}

	return sm.generateUserJWT(record)
}

// generateUserJWT creates a user JWT based on account context and role permissions.
func (sm *Manager) generateUserJWT(record *core.Record) error {
	account, err := sm.app.FindRecordById(sm.options.AccountCollectionName, record.GetString("account_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find account %s", record.GetString("account_id"))
	}

	role, err := sm.app.FindRecordById(sm.options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find role %s", record.GetString("role_id"))
	}

	user := pbtypes.RecordToUserModel(record, sm.options.EncryptionKey)
	accountModel := pbtypes.RecordToAccountModel(account, sm.options.EncryptionKey)
	roleModel := pbtypes.RecordToRoleModel(role)

	jwtValue, err := sm.jwtGen.GenerateUserJWT(user, accountModel, roleModel)
	if err != nil {
		return utils.WrapError(err, "failed to generate user JWT")
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	credsFile, err := sm.jwtGen.GenerateCredsFile(user)
	if err != nil {
		return utils.WrapError(err, "failed to generate creds file")
	}

	record.Set("creds_file", credsFile)
	return nil
}

// regenerateUserJWT recreates user JWT when role or account changes.
func (sm *Manager) regenerateUserJWT(record *core.Record) error {
	record.Set("jwt", "")
	record.Set("creds_file", "")
	return sm.generateUserJWT(record)
}

// userJWTFieldsChanged reports whether any field that is embedded in the user JWT
// differs from the previously saved version of the record.
func userJWTFieldsChanged(record *core.Record) bool {
	original := record.Original()
	jwtFields := []string{
		"nats_username", "account_id", "role_id", "bearer_token", "jwt_expires_at",
		"publish_permissions", "subscribe_permissions",
		"publish_deny_permissions", "subscribe_deny_permissions",
	}
	for _, field := range jwtFields {
		if fmt.Sprintf("%v", record.Get(field)) != fmt.Sprintf("%v", original.Get(field)) {
			return true
		}
	}
	return false
}

// refreshSystemUserCredentials pushes updated credentials to the NATS connection
// manager when the system user's JWT has been regenerated, so the persistent
// connection doesn't keep authenticating with a stale JWT.
func (sm *Manager) refreshSystemUserCredentials(record *core.Record) {
	if record.GetString("nats_username") != "sys" || record.GetString("account_id") != sm.systemAccountID {
		return
	}

	user := pbtypes.RecordToUserModel(record, sm.options.EncryptionKey)
	if user.JWT == "" || user.Seed == "" {
		return
	}

	if err := sm.publisher.UpdateCredentials(user.JWT, user.Seed); err != nil {
		sm.logger.Warning("Failed to refresh system user NATS credentials: %v", err)
	}
}

// getSystemOperator retrieves the system operator record for JWT signing operations.
func (sm *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := sm.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system operator records")
	}
	if len(records) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system operator not found - ensure Setup() completed successfully"),
			"system operator lookup failed")
	}

	record := records[0]
	operator := pbtypes.RecordToOperatorModel(record, sm.options.EncryptionKey)

	if err := utils.ValidateRequired(operator.PublicKey, "operator public key"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}
	if operator.LatestSigningKey() == nil {
		return nil, utils.WrapError(fmt.Errorf("operator has no signing keys"), "invalid system operator")
	}

	return operator, nil
}

// hasPriorState reports whether a record already existed in a meaningful state
// before this update.
//
// The create hooks generate keys and save the record a second time, and that save
// runs the update hooks. On such a record Original() holds PocketBase's field zero
// values rather than a stored row, so an account or user created with active=true
// would otherwise look exactly like an inactive->active edge. A record that has been
// through key generation always carries a public key; one being finished by the
// create hook does not.
func hasPriorState(record *core.Record) bool {
	return record.Original().GetString("public_key") != ""
}

// signingKeysRemoved reports whether the update dropped any signing key the account
// held before it. Removal invalidates every user JWT signed by that key.
func signingKeysRemoved(record *core.Record) bool {
	before := pbtypes.RecordSigningPublicKeys(record.Original())
	after := pbtypes.RecordSigningPublicKeys(record)
	for key := range before {
		if !after[key] {
			return true
		}
	}
	return false
}

// regenerateUsersInAccount reissues JWTs for every user in an account. Called after
// a signing key is removed or rotated, since those JWTs are no longer valid.
func (sm *Manager) regenerateUsersInAccount(accountID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"account_id": accountID})
	if err != nil {
		return utils.WrapErrorf(err, "failed to find users in account %s", accountID)
	}

	reissued := 0
	for _, user := range users {
		// A suspended user has no valid credentials by design — leave them revoked.
		if !user.GetBool("active") {
			continue
		}
		if err := sm.regenerateUserJWT(user); err != nil {
			sm.logger.Warning("Failed to reissue JWT for user %s: %v", user.Id, err)
			continue
		}
		if err := sm.app.Save(user); err != nil {
			sm.logger.Warning("Failed to save user %s: %v", user.Id, err)
			continue
		}
		reissued++
	}

	if reissued > 0 {
		sm.logger.Info("Reissued credentials for %d user(s) in account %s — clients must download the new creds file",
			reissued, accountID)
	}
	return nil
}

// regenerateUsersWithRole updates JWTs for all active users sharing a specific role.
func (sm *Manager) regenerateUsersWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return utils.WrapErrorf(err, "failed to find users with role %s", roleID)
	}

	for _, user := range users {
		// A suspended user has no working credentials by design: clearing `active`
		// revoked their key and deliberately did not reissue. Reissuing here would
		// stamp a JWT after the revocation cutoff — valid again — and write a fresh
		// creds_file for them to download, silently undoing the suspension from an
		// unrelated edit to a role they happen to share. regenerateUsersInAccount
		// skips them for the same reason.
		if !user.GetBool("active") {
			continue
		}
		if err := sm.regenerateUserJWT(user); err != nil {
			sm.logger.Warning("Failed to regenerate JWT for user %s: %v", user.Id, err)
			continue
		}
		if err := sm.app.Save(user); err != nil {
			sm.logger.Warning("Failed to save user %s: %v", user.Id, err)
		}
	}

	return nil
}

// scheduleAccountsWithRole schedules NATS publishing for all accounts containing users with a specific role.
func (sm *Manager) scheduleAccountsWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return utils.WrapErrorf(err, "failed to find users with role %s", roleID)
	}

	accountIDs := make(map[string]bool)
	for _, user := range users {
		accountID := user.GetString("account_id")
		if accountID != "" {
			accountIDs[accountID] = true
		}
	}

	for accountID := range accountIDs {
		sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
	}

	return nil
}
