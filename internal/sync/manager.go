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
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager orchestrates real-time synchronization between PocketBase record changes and NATS.
type Manager struct {
	app         *pocketbase.PocketBase
	jwtGen      *jwt.Generator
	nkeyManager *nkey.Manager
	publisher   *publisher.Manager
	options     pbtypes.Options
	logger      *utils.Logger
	
	// Debouncing state
	timer      *time.Timer
	timerMutex sync.Mutex
	
	// Processing state  
	isProcessing    bool
	processingMutex sync.Mutex
}

// NewManager creates a new sync manager with all required dependencies.
func NewManager(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, 
	publisher *publisher.Manager, options pbtypes.Options) *Manager {
	return &Manager{
		app:         app,
		jwtGen:      jwtGen,
		nkeyManager: nkeyManager,
		publisher:   publisher,
		options:     options,
		logger:      utils.NewLogger(options.LogToConsole),
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
		if e.Record.GetString("name") == "System Account" {
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
		if e.Record.GetString("name") == "System Account" {
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
		if e.Record.GetString("name") == "System Account" {
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

	oldSigningPublicKey := record.GetString("signing_public_key")

	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	if err := sm.generateAccountJWT(record); err != nil {
		return utils.WrapError(err, "failed to regenerate account JWT with new signing keys")
	}

	sm.logger.Success("Account signing keys rotated successfully for %s", record.GetString("name"))
	sm.logger.Info("   Old signing key: %s", utils.TruncateString(oldSigningPublicKey, 20))
	sm.logger.Info("   New signing key: %s", utils.TruncateString(signingPublic, 20))
	sm.logger.Warning("   All user JWTs in this account are now invalid and must be regenerated")

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
	record.Set("private_key", privateKey)
	record.Set("seed", seed)
	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	return sm.generateAccountJWT(record)
}

// generateAccountJWT creates an account JWT using the system operator's signing key.
func (sm *Manager) generateAccountJWT(record *core.Record) error {
	operator, err := sm.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator")
	}

	account := sm.recordToAccountModel(record)

	jwtValue, err := sm.jwtGen.GenerateAccountJWT(account, operator.SigningSeed)
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
	record.Set("private_key", privateKey)
	record.Set("seed", seed)

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

	user := sm.recordToUserModel(record)
	accountModel := sm.recordToAccountModel(account)
	roleModel := sm.recordToRoleModel(role)

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

// recordToUserModel converts PocketBase user record to internal user model.
func (sm *Manager) recordToUserModel(record *core.Record) *pbtypes.NatsUserRecord {
	return &pbtypes.NatsUserRecord{
		ID:           record.Id,
		NatsUsername: record.GetString("nats_username"),
		PublicKey:    record.GetString("public_key"),
		Seed:         record.GetString("seed"),
		AccountID:    record.GetString("account_id"),
		RoleID:       record.GetString("role_id"),
		JWT:          record.GetString("jwt"),
		BearerToken:  record.GetBool("bearer_token"),
		Active:       record.GetBool("active"),
		Regenerate:   record.GetBool("regenerate"),
	}
}

// recordToAccountModel converts PocketBase account record to internal account model.
func (sm *Manager) recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:                        record.Id,
		Name:                      record.GetString("name"),
		PublicKey:                 record.GetString("public_key"),
		SigningPublicKey:          record.GetString("signing_public_key"),
		SigningSeed:               record.GetString("signing_seed"),
		JWT:                       record.GetString("jwt"),
		MaxConnections:            int64(record.GetInt("max_connections")),
		MaxSubscriptions:          int64(record.GetInt("max_subscriptions")),
		MaxData:                   int64(record.GetInt("max_data")),
		MaxPayload:                int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:   int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage: int64(record.GetInt("max_jetstream_memory_storage")),
	}
}

// recordToRoleModel converts PocketBase role record to internal role model.
// Uses proper JSON marshaling for JSON fields.
func (sm *Manager) recordToRoleModel(record *core.Record) *pbtypes.RoleRecord {
	role := &pbtypes.RoleRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		Description:      record.GetString("description"),
		IsDefault:        record.GetBool("is_default"),
		AllowResponse:    record.GetBool("allow_response"),
		AllowResponseMax: record.GetInt("allow_response_max"),
		AllowResponseTTL: record.GetInt("allow_response_ttl"),
		MaxSubscriptions: int64(record.GetInt("max_subscriptions")),
		MaxData:          int64(record.GetInt("max_data")),
		MaxPayload:       int64(record.GetInt("max_payload")),
		Created:          record.GetDateTime("created").Time(),
		Updated:          record.GetDateTime("updated").Time(),
	}

	// Marshal JSON fields from record.Get() which returns the actual data structure
	if val := record.Get("publish_permissions"); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			role.PublishPermissions = bytes
		}
	}
	if val := record.Get("subscribe_permissions"); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			role.SubscribePermissions = bytes
		}
	}
	if val := record.Get("publish_deny_permissions"); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			role.PublishDenyPermissions = bytes
		}
	}
	if val := record.Get("subscribe_deny_permissions"); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			role.SubscribeDenyPermissions = bytes
		}
	}

	return role
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
	operator := &pbtypes.SystemOperatorRecord{
		ID:          record.Id,
		Name:        record.GetString("name"),
		PublicKey:   record.GetString("public_key"),
		SigningSeed: record.GetString("signing_seed"),
	}

	if err := utils.ValidateRequired(operator.PublicKey, "operator public key"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}
	if err := utils.ValidateRequired(operator.SigningSeed, "operator signing seed"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
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
