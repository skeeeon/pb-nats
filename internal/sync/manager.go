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

// Manager manages the synchronization between PocketBase and NATS
type Manager struct {
	app         *pocketbase.PocketBase
	jwtGen      *jwt.Generator
	nkeyManager *nkey.Manager
	publisher   *publisher.Manager
	options     pbtypes.Options
	logger      *utils.Logger
	
	// Debouncing
	timer       *time.Timer
	timerMutex  sync.Mutex
	
	// Processing state
	isProcessing     bool
	processingMutex  sync.Mutex
}

// NewManager creates a new sync manager
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

// SetupHooks sets up all the PocketBase hooks for synchronization
// This is separated from system component initialization to ensure proper order
func (sm *Manager) SetupHooks() error {
	sm.logger.Info("Setting up PocketBase hooks for NATS sync...")

	// Setup hooks for each collection type
	sm.setupAccountHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()

	sm.logger.Success("PocketBase hooks configured for NATS sync")

	return nil
}

// setupAccountHooks sets up hooks for account changes
func (sm *Manager) setupAccountHooks() {
	// Account creation
	sm.app.OnRecordCreateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.AccountCollectionName {
			return e.Next()
		}

		if err := sm.generateAccountKeys(e.Record); err != nil {
			return utils.WrapError(err, "failed to generate account keys")
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountCreate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Account updates
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.AccountCollectionName {
			return e.Next()
		}

		// Skip system account to prevent infinite loops
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

		// Prevent deletion of system account
		if e.Record.GetString("name") == "System Account" {
			return utils.WrapError(fmt.Errorf("cannot delete system account"), "account deletion validation failed")
		}

		if sm.shouldHandleEvent(sm.options.AccountCollectionName, pbtypes.EventTypeAccountDelete) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionDelete)
		}
		return e.Next()
	})
}

// setupUserHooks sets up hooks for user changes
func (sm *Manager) setupUserHooks() {
	// User creation
	sm.app.OnRecordCreateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if err := sm.generateUserKeys(e.Record); err != nil {
			return utils.WrapError(err, "failed to generate user keys")
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserCreate) {
			accountID := e.Record.GetString("account_id")
			if accountID != "" {
				sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User updates - handle regenerate field and normal updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// Check if regenerate field was set to true
		if e.Record.GetBool("regenerate") {
			// Clear the regenerate flag immediately to prevent loops
			e.Record.Set("regenerate", false)
			
			// Force JWT regeneration
			if err := sm.regenerateUserJWT(e.Record); err != nil {
				return utils.WrapError(err, "failed to regenerate user JWT")
			}
			
			sm.logger.Info("JWT regenerated for user %s due to regenerate flag", e.Record.GetString("nats_username"))
		} else {
			// Regular update - regenerate JWT to ensure consistency
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

// setupRoleHooks sets up hooks for role changes
func (sm *Manager) setupRoleHooks() {
	// Role updates affect all users with that role
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.RoleCollectionName, pbtypes.EventTypeRoleUpdate) {
			// Find all users with this role and regenerate their JWTs
			if err := sm.regenerateUsersWithRole(e.Record.Id); err != nil {
				sm.logger.Warning("Failed to regenerate users with role %s: %v", e.Record.Id, err)
			}
			
			// Trigger publish for all affected accounts
			if err := sm.scheduleAccountsWithRole(e.Record.Id); err != nil {
				sm.logger.Warning("Failed to schedule accounts with role %s: %v", e.Record.Id, err)
			}
		}
		return e.Next()
	})
}

// shouldHandleEvent determines if an event should be handled based on the event filter
func (sm *Manager) shouldHandleEvent(collectionName, eventType string) bool {
	if sm.options.EventFilter != nil {
		return sm.options.EventFilter(collectionName, eventType)
	}
	return true
}

// scheduleSync schedules a sync operation with debouncing
func (sm *Manager) scheduleSync(accountID, action string) {
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	// Cancel any existing timer
	if sm.timer != nil {
		sm.timer.Stop()
	}

	// Queue the operation immediately
	if err := sm.publisher.QueueAccountUpdate(accountID, action); err != nil {
		sm.logger.Warning("Failed to queue account update for account %s: %v", accountID, err)
	}

	// Schedule processing after debounce interval
	sm.timer = time.AfterFunc(sm.options.DebounceInterval, func() {
		if err := sm.publisher.ProcessPublishQueue(); err != nil {
			sm.logger.Warning("Error processing publish queue: %v", err)
		}
	})
}

// generateAccountKeys generates keys and JWT for an account
func (sm *Manager) generateAccountKeys(record *core.Record) error {
	// Skip if keys already exist
	if record.GetString("public_key") != "" {
		return nil
	}

	// Generate account keys
	seed, public, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate account key pair")
	}

	// Get private keys
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return utils.WrapError(err, "failed to get signing private key from seed")
	}

	// Set the keys
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)
	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	// Generate JWT
	return sm.generateAccountJWT(record)
}

// generateAccountJWT generates JWT for an account
func (sm *Manager) generateAccountJWT(record *core.Record) error {
	// Get system operator
	operator, err := sm.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator")
	}

	// Create account record
	account := &pbtypes.AccountRecord{
		PublicKey:        record.GetString("public_key"),
		SigningPublicKey: record.GetString("signing_public_key"),
		Name:             record.GetString("name"),
	}

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateAccountJWT(account, operator.SigningSeed)
	if err != nil {
		return utils.WrapError(err, "failed to generate account JWT")
	}

	record.Set("jwt", jwtValue)
	return nil
}

// generateUserKeys generates keys and JWT for a user
func (sm *Manager) generateUserKeys(record *core.Record) error {
	// Skip if keys already exist
	if record.GetString("public_key") != "" {
		return nil
	}

	// Generate user keys
	seed, public, err := sm.nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate user key pair")
	}

	// Get private key
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
	}

	// Set the keys
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)

	// Generate JWT and creds file
	return sm.generateUserJWT(record)
}

// generateUserJWT generates JWT and creds file for a user
func (sm *Manager) generateUserJWT(record *core.Record) error {
	// Get account and role
	account, err := sm.app.FindRecordById(sm.options.AccountCollectionName, record.GetString("account_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find account %s", record.GetString("account_id"))
	}

	role, err := sm.app.FindRecordById(sm.options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find role %s", record.GetString("role_id"))
	}

	// Convert to models
	user := sm.recordToUserModel(record)
	accountModel := sm.recordToAccountModel(account)
	roleModel := sm.recordToRoleModel(role)

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateUserJWT(user, accountModel, roleModel)
	if err != nil {
		return utils.WrapError(err, "failed to generate user JWT")
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	// Generate creds file
	credsFile, err := sm.jwtGen.GenerateCredsFile(user)
	if err != nil {
		return utils.WrapError(err, "failed to generate creds file")
	}

	record.Set("creds_file", credsFile)
	return nil
}

// regenerateUserJWT regenerates JWT for a user (when role/account changes or regenerate flag is set)
func (sm *Manager) regenerateUserJWT(record *core.Record) error {
	// Clear existing JWT and creds
	record.Set("jwt", "")
	record.Set("creds_file", "")
	
	// Regenerate
	return sm.generateUserJWT(record)
}

// Helper methods to convert records to models
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

func (sm *Manager) recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		PublicKey:        record.GetString("public_key"),
		SigningSeed:      record.GetString("signing_seed"),
		JWT:              record.GetString("jwt"),
	}
}

func (sm *Manager) recordToRoleModel(record *core.Record) *pbtypes.RoleRecord {
	return &pbtypes.RoleRecord{
		ID:                   record.Id,
		Name:                 record.GetString("name"),
		PublishPermissions:   []byte(record.GetString("publish_permissions")),
		SubscribePermissions: []byte(record.GetString("subscribe_permissions")),
		MaxConnections:       int64(record.GetInt("max_connections")),
		MaxData:              int64(record.GetInt("max_data")),
		MaxPayload:           int64(record.GetInt("max_payload")),
	}
}

// getSystemOperator gets the system operator with consistent error handling
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
		ID:                record.Id,
		Name:              record.GetString("name"),
		PublicKey:         record.GetString("public_key"),
		SigningSeed:       record.GetString("signing_seed"),
	}

	// Enhanced validation
	if err := utils.ValidateRequired(operator.PublicKey, "operator public key"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}
	
	if err := utils.ValidateRequired(operator.SigningSeed, "operator signing seed"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}

	return operator, nil
}

// regenerateUsersWithRole regenerates JWTs for all users with a specific role
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

// scheduleAccountsWithRole schedules sync for all accounts that have users with a specific role
func (sm *Manager) scheduleAccountsWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return utils.WrapErrorf(err, "failed to find users with role %s", roleID)
	}

	// Collect unique account IDs
	accountIDs := make(map[string]bool)
	for _, user := range users {
		accountID := user.GetString("account_id")
		if accountID != "" {
			accountIDs[accountID] = true
		}
	}

	// Schedule sync for each account
	for accountID := range accountIDs {
		sm.scheduleSync(accountID, pbtypes.PublishActionUpsert)
	}

	return nil
}
