// Package sync handles synchronization between PocketBase and NATS
package sync

import (
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	"github.com/skeeeon/pb-nats/internal/publisher"
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager orchestrates real-time synchronization between PocketBase record changes and NATS.
type Manager struct {
	app              *pocketbase.PocketBase
	jwtGen           *jwt.Generator
	nkeyManager      *nkey.Manager
	publisher        *publisher.Manager
	options          pbtypes.Options
	logger           *utils.Logger
	systemAccountID  string

	// Debouncing state
	timer      *time.Timer
	timerMutex sync.Mutex

	// Processing state
	isProcessing    bool
	processingMutex sync.Mutex
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

// SetupHooks registers PocketBase event hooks for real-time NATS synchronization.
func (sm *Manager) SetupHooks() error {
	sm.logger.Info("Setting up PocketBase hooks for NATS sync...")

	sm.setupAccountHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()

	sm.logger.Success("PocketBase hooks configured for NATS sync")
	return nil
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

		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountCreate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Account updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.AccountCollectionName {
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

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}
		if e.Record.Id == sm.systemAccountID {
			return e.Next()
		}
		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountUpdate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Account deletion
	sm.app.OnRecordDeleteRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.AccountCollectionName {
			return e.Next()
		}
		if e.Record.Id == sm.systemAccountID {
			return utils.WrapError(fmt.Errorf("cannot delete system account"), "account deletion validation failed")
		}
		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountDelete) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionDelete)
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

	// User updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if e.Record.GetBool("regenerate") {
			e.Record.Set("regenerate", false)
			if err := sm.regenerateUserJWT(e.Record); err != nil {
				return utils.WrapError(err, "failed to regenerate user JWT")
			}
			sm.logger.Info("JWT regenerated for user %s due to regenerate flag", e.Record.GetString("nats_username"))
		} else {
			if err := sm.regenerateUserJWT(e.Record); err != nil {
				return utils.WrapError(err, "failed to regenerate user JWT")
			}
		}
		return e.Next()
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserUpdate) {
			accountID := e.Record.GetString("account_id")
			if accountID != "" {
				sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User deletion
	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserDelete) {
			accountID := e.Record.GetString("account_id")
			if accountID != "" {
				sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
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
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	if sm.timer != nil {
		sm.timer.Stop()
	}

	if err := sm.publisher.QueueAccountUpdate(accountID, action); err != nil {
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

	jwtValue, err := sm.jwtGen.GenerateAccountJWT(account, latestKey.Seed)
	if err != nil {
		return utils.WrapError(err, "failed to generate account JWT")
	}

	record.Set("jwt", jwtValue)
	return nil
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

// regenerateUsersWithRole updates JWTs for all users sharing a specific role.
func (sm *Manager) regenerateUsersWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return utils.WrapErrorf(err, "failed to find users with role %s", roleID)
	}

	for _, user := range users {
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
