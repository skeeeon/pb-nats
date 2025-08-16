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
// This is the central component that responds to database events and triggers JWT regeneration
// and NATS publishing operations.
//
// SYNCHRONIZATION STRATEGY:
// - PocketBase Record Change → JWT Regeneration → Account Publishing → NATS Update
// - Debouncing prevents excessive NATS traffic during rapid changes
// - Queue-based publishing ensures reliability during NATS outages
//
// EVENT HANDLING:
// - Account changes: Regenerate account JWT, publish to NATS
// - User changes: Regenerate user JWT, publish account to NATS  
// - Role changes: Regenerate all affected user JWTs, publish all affected accounts
type Manager struct {
	app         *pocketbase.PocketBase    // PocketBase application instance
	jwtGen      *jwt.Generator            // JWT generation service
	nkeyManager *nkey.Manager             // NKey management service
	publisher   *publisher.Manager        // NATS publishing service
	options     pbtypes.Options           // Configuration options
	logger      *utils.Logger             // Logger for consistent output
	
	// Debouncing state
	timer       *time.Timer  // Timer for debounced operations
	timerMutex  sync.Mutex   // Protects timer access
	
	// Processing state  
	isProcessing     bool        // Flag to prevent concurrent processing
	processingMutex  sync.Mutex  // Protects processing flag
}

// NewManager creates a new sync manager with all required dependencies.
//
// PARAMETERS:
//   - app: PocketBase application instance for database access
//   - jwtGen: JWT generator for creating NATS JWTs
//   - nkeyManager: NKey manager for key generation
//   - publisher: NATS publisher for account updates
//   - options: Configuration options
//
// RETURNS:
// - Manager instance ready for hook setup
//
// INITIALIZATION:
// The manager is created but hooks are not yet registered. Call SetupHooks()
// after all system components are initialized to avoid race conditions.
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
// This is called after all system components are initialized to avoid race conditions.
//
// HOOK CATEGORIES:
// - Account hooks: Handle account lifecycle and key rotation
// - User hooks: Handle user lifecycle and JWT regeneration
// - Role hooks: Handle permission changes affecting multiple users
//
// TIMING:
// Called after system initialization to ensure all components are ready
// before processing database events.
//
// RETURNS:
// - nil on successful hook registration
// - error if hook setup fails
//
// SIDE EFFECTS:
// - Registers multiple PocketBase event hooks
// - Enables real-time synchronization
func (sm *Manager) SetupHooks() error {
	sm.logger.Info("Setting up PocketBase hooks for NATS sync...")

	// Setup hooks for each collection type
	sm.setupAccountHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()

	sm.logger.Success("PocketBase hooks configured for NATS sync")

	return nil
}

// setupAccountHooks registers hooks for account lifecycle and management operations.
//
// ACCOUNT EVENT HANDLING:
// - Creation: Generate keys and JWT, schedule NATS publishing
// - Updates: Regenerate JWT, handle signing key rotation, schedule publishing
// - Deletion: Remove account from NATS (with system account protection)
//
// KEY ROTATION LOGIC:
// The rotate_keys boolean field triggers emergency signing key rotation:
// 1. Field set to true → generate new signing keys
// 2. Regenerate account JWT with new keys
// 3. All user JWTs become invalid immediately (signed with old key)
// 4. Clear rotate_keys flag to prevent loops
//
// SIDE EFFECTS:
// - Registers OnRecordCreateRequest, OnRecordAfterCreateSuccess
// - Registers OnRecordUpdateRequest, OnRecordAfterUpdateSuccess  
// - Registers OnRecordDeleteRequest
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

	// Account updates - handle rotate_keys field and normal updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.AccountCollectionName {
			return e.Next()
		}

		// Check if rotate_keys field was set to true
		if e.Record.GetBool("rotate_keys") {
			// Clear the rotate_keys flag immediately to prevent loops
			e.Record.Set("rotate_keys", false)
			
			// Perform signing key rotation
			if err := sm.rotateAccountSigningKeys(e.Record); err != nil {
				return utils.WrapError(err, "failed to rotate account signing keys")
			}
			
			sm.logger.Info("Signing keys rotated for account %s - all user JWTs in this account are now invalid", 
				e.Record.GetString("name"))
		} else {
			// Regular update - regenerate JWT to ensure consistency
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

// setupUserHooks registers hooks for user lifecycle and JWT management operations.
//
// USER EVENT HANDLING:
// - Creation: Generate keys and JWT, schedule account publishing
// - Updates: Regenerate JWT (normal or forced via regenerate flag), schedule account publishing
// - Deletion: Schedule account publishing (user removed from account)
//
// REGENERATE FLAG LOGIC:
// The regenerate boolean field forces JWT regeneration:
// 1. Field set to true → regenerate user JWT and .creds file
// 2. Clear regenerate flag to prevent loops
// 3. Use case: credential rotation, permission updates, security incidents
//
// ACCOUNT PUBLISHING:
// User changes trigger account publishing because the account JWT needs to be
// republished to NATS to reflect the updated user roster.
//
// SIDE EFFECTS:
// - Registers OnRecordCreateRequest, OnRecordAfterCreateSuccess
// - Registers OnRecordUpdateRequest, OnRecordAfterUpdateSuccess
// - Registers OnRecordAfterDeleteSuccess
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

// setupRoleHooks registers hooks for role permission changes affecting multiple users.
//
// ROLE EVENT HANDLING:
// Role changes have broad impact because multiple users across multiple accounts
// may share the same role:
//
// ROLE UPDATE PROCESS:
// 1. Role permissions change
// 2. Find all users with this role (across all accounts)
// 3. Regenerate JWT for each affected user
// 4. Schedule publishing for all affected accounts
//
// PERFORMANCE CONSIDERATION:
// Role changes can be expensive operations if many users share the role.
// The system processes all affected users synchronously during the update.
//
// SIDE EFFECTS:
// - Registers OnRecordAfterUpdateSuccess
// - May regenerate many user JWTs
// - May schedule many account publishing operations
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

// rotateAccountSigningKeys performs emergency signing key rotation for an account.
// This is a critical security operation that immediately invalidates all user JWTs.
//
// SECURITY IMPACT:
// - Generates new account signing key pair
// - Preserves account identity (main account key unchanged)
// - All user JWTs become invalid immediately (signed with old key)
// - Users must re-authenticate to get new JWTs
//
// WHEN TO USE:
// - Account compromise detected
// - Suspicious NATS activity
// - Periodic security hardening
// - Before sensitive operations
//
// ROTATION PROCESS:
// 1. Generate new signing key pair (preserve account identity)
// 2. Update account record with new signing keys
// 3. Regenerate account JWT with new signing keys
// 4. Users lose NATS access until they get fresh JWTs from PocketBase
//
// PARAMETERS:
//   - record: Account record to rotate keys for
//
// RETURNS:
// - nil on successful rotation
// - error if key generation or JWT creation fails
//
// SIDE EFFECTS:
// - Modifies account record with new signing keys
// - Immediately invalidates all user JWTs for this account
func (sm *Manager) rotateAccountSigningKeys(record *core.Record) error {
	sm.logger.Info("Rotating signing keys for account %s...", record.GetString("name"))

	// Generate new signing key pair only (ignore main account keys to preserve account identity)
	_, _, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate new account signing key pair")
	}

	// Get signing private key
	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return utils.WrapError(err, "failed to get signing private key from seed")
	}

	// Store old keys for logging purposes
	oldSigningPublicKey := record.GetString("signing_public_key")

	// Update record with new signing keys (keep main account keys unchanged)
	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	// Regenerate account JWT with new signing keys
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
//
// EVENT FILTERING:
// Allows selective event processing based on collection and event type.
// Useful for debugging, testing, or custom workflows.
//
// PARAMETERS:
//   - collectionName: Name of collection where event occurred
//   - eventType: Type of event (create, update, delete, etc.)
//
// RETURNS:
// - true if event should be processed
// - false if event should be ignored
//
// DEFAULT BEHAVIOR:
// If no filter configured, all events are processed.
func (sm *Manager) shouldHandleEvent(collectionName, eventType string) bool {
	if sm.options.EventFilter != nil {
		return sm.options.EventFilter(collectionName, eventType)
	}
	return true
}

// scheduleSync schedules a NATS publishing operation with debouncing to prevent excessive traffic.
//
// DEBOUNCING STRATEGY:
// - Multiple rapid changes → single delayed operation
// - Timer reset on each new change
// - Prevents NATS overload during bulk operations
//
// SCHEDULING PROCESS:
// 1. Queue operation immediately
// 2. Start/reset debounce timer
// 3. Process queue when timer expires
//
// PARAMETERS:
//   - accountID: Database ID of account to publish
//   - action: Action type (upsert or delete)
//
// SIDE EFFECTS:
// - Queues operation immediately
// - Starts/resets debounce timer
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

// generateAccountKeys generates key pairs and JWT for a new account record.
//
// ACCOUNT KEY STRUCTURE:
// - Main key pair: Account identity (public key is account ID)
// - Signing key pair: Used to sign user JWTs
// - JWT: Account definition for NATS server
//
// PARAMETERS:
//   - record: Account record to populate with keys and JWT
//
// BEHAVIOR:
// - Skips if keys already exist (idempotent)
// - Generates main account key pair
// - Generates signing key pair
// - Creates account JWT
//
// RETURNS:
// - nil on success
// - error if key generation or JWT creation fails
//
// SIDE EFFECTS:
// - Modifies account record with generated keys and JWT
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

// generateAccountJWT creates an account JWT using the system operator's signing key.
//
// JWT GENERATION PROCESS:
// 1. Retrieve system operator from database
// 2. Create account model from record data
// 3. Generate JWT using operator's signing key
// 4. Store JWT in account record
//
// PARAMETERS:
//   - record: Account record to generate JWT for
//
// RETURNS:
// - nil on success
// - error if operator lookup or JWT generation fails
//
// SIDE EFFECTS:
// - Updates account record with generated JWT
func (sm *Manager) generateAccountJWT(record *core.Record) error {
	// Get system operator
	operator, err := sm.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator")
	}

	// Create account record
	account := sm.recordToAccountModel(record)

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateAccountJWT(account, operator.SigningSeed)
	if err != nil {
		return utils.WrapError(err, "failed to generate account JWT")
	}

	record.Set("jwt", jwtValue)
	return nil
}

// generateUserKeys generates key pairs and JWT for a new user record.
//
// USER KEY STRUCTURE:
// - User key pair: User identity for NATS authentication
// - JWT: User permissions and limits signed by account
// - Creds file: Complete authentication file for NATS clients
//
// PARAMETERS:
//   - record: User record to populate with keys and JWT
//
// BEHAVIOR:
// - Skips if keys already exist (idempotent)
// - Generates user key pair
// - Creates user JWT based on account and role
// - Generates .creds file for client use
//
// RETURNS:
// - nil on success
// - error if key generation or JWT creation fails
//
// SIDE EFFECTS:
// - Modifies user record with generated keys, JWT, and .creds file
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

// generateUserJWT creates a user JWT based on account context and role permissions.
//
// JWT GENERATION PROCESS:
// 1. Retrieve account and role records from database
// 2. Convert records to model types
// 3. Generate user JWT with role-based permissions
// 4. Generate .creds file containing JWT and user key
//
// PARAMETERS:
//   - record: User record to generate JWT for
//
// RETURNS:
// - nil on success
// - error if account/role lookup or JWT generation fails
//
// SIDE EFFECTS:
// - Updates user record with JWT and .creds file
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

// regenerateUserJWT recreates user JWT when role or account changes or manual regeneration triggered.
//
// USE CASES:
// - Role permissions changed
// - Account signing key rotated
// - Manual regeneration via regenerate flag
// - User settings updated
//
// REGENERATION PROCESS:
// 1. Clear existing JWT and .creds file
// 2. Generate new JWT with current permissions
// 3. Generate new .creds file
//
// PARAMETERS:
//   - record: User record to regenerate JWT for
//
// RETURNS:
// - nil on success
// - error if JWT regeneration fails
//
// SIDE EFFECTS:
// - Updates user record with new JWT and .creds file
func (sm *Manager) regenerateUserJWT(record *core.Record) error {
	// Clear existing JWT and creds
	record.Set("jwt", "")
	record.Set("creds_file", "")
	
	// Regenerate
	return sm.generateUserJWT(record)
}

// Helper methods to convert PocketBase records to internal model types

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
// Updated to include the new account limit fields with proper int64 conversion.
func (sm *Manager) recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		PublicKey:        record.GetString("public_key"),
		SigningPublicKey: record.GetString("signing_public_key"),
		SigningSeed:      record.GetString("signing_seed"),
		JWT:              record.GetString("jwt"),
		
		// Account-level limits (FIXED: using int64(record.GetInt()) instead of record.GetInt64())
		MaxConnections:                int64(record.GetInt("max_connections")),
		MaxSubscriptions:              int64(record.GetInt("max_subscriptions")),
		MaxData:                       int64(record.GetInt("max_data")),
		MaxPayload:                    int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:       int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage:     int64(record.GetInt("max_jetstream_memory_storage")),
	}
}

// recordToRoleModel converts PocketBase role record to internal role model.
// Updated to use max_subscriptions instead of max_connections.
func (sm *Manager) recordToRoleModel(record *core.Record) *pbtypes.RoleRecord {
	return &pbtypes.RoleRecord{
		ID:                   record.Id,
		Name:                 record.GetString("name"),
		PublishPermissions:   []byte(record.GetString("publish_permissions")),
		SubscribePermissions: []byte(record.GetString("subscribe_permissions")),
		MaxSubscriptions:     int64(record.GetInt("max_subscriptions")), // FIXED: renamed from max_connections
		MaxData:              int64(record.GetInt("max_data")),
		MaxPayload:           int64(record.GetInt("max_payload")),
	}
}

// getSystemOperator retrieves the system operator record for JWT signing operations.
//
// SYSTEM OPERATOR LOOKUP:
// - Assumes single operator record exists
// - Validates critical fields (public key, signing seed)
// - Used for signing account JWTs
//
// RETURNS:
// - SystemOperatorRecord with validated fields
// - error if operator not found or invalid
//
// THREAD SAFETY: Safe for concurrent access (read-only operation)
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

// regenerateUsersWithRole updates JWTs for all users sharing a specific role.
// This is called when role permissions change and affects users across multiple accounts.
//
// BULK REGENERATION PROCESS:
// 1. Find all users with the specified role
// 2. Regenerate JWT for each user
// 3. Save updated user records
// 4. Log warnings for any failures (continues processing)
//
// PARAMETERS:
//   - roleID: Database ID of role that changed
//
// RETURNS:
// - nil on successful processing (individual user failures logged as warnings)
// - error if user lookup fails
//
// SIDE EFFECTS:
// - Updates multiple user records with new JWTs
// - May impact users across multiple accounts
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
// This ensures all affected accounts are updated in NATS after role changes.
//
// ACCOUNT SCHEDULING PROCESS:
// 1. Find all users with the specified role
// 2. Extract unique account IDs
// 3. Schedule publishing for each affected account
//
// PARAMETERS:
//   - roleID: Database ID of role that changed
//
// RETURNS:
// - nil on successful scheduling
// - error if user lookup fails
//
// SIDE EFFECTS:
// - Schedules multiple account publishing operations
// - May trigger NATS updates for multiple accounts
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
