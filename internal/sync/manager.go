// Package sync handles synchronization between PocketBase and NATS
package sync

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	"github.com/skeeeon/pb-nats/internal/publisher"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager manages the synchronization between PocketBase and NATS
type Manager struct {
	app         *pocketbase.PocketBase
	jwtGen      *jwt.Generator
	nkeyManager *nkey.Manager
	publisher   *publisher.Manager
	options     pbtypes.Options
	
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
	}
}

// Setup sets up all the PocketBase hooks for synchronization
func (sm *Manager) Setup() error {
	// Initialize system operator and account if needed
	if err := sm.initializeSystemComponents(); err != nil {
		return fmt.Errorf("failed to initialize system components: %w", err)
	}

	// Setup hooks for each collection
	sm.setupOrganizationHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()

	if sm.options.LogToConsole {
		log.Printf("NATS sync manager initialized successfully")
	}

	return nil
}

// initializeSystemComponents creates the system operator and SYS account if they don't exist
func (sm *Manager) initializeSystemComponents() error {
	// Check if system operator exists
	operatorRecords, err := sm.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find system operator records: %w", err)
	}

	var operator *pbtypes.SystemOperatorRecord
	if len(operatorRecords) == 0 {
		// Create system operator
		operator, err = sm.createSystemOperator()
		if err != nil {
			return fmt.Errorf("failed to create system operator: %w", err)
		}
	} else {
		// Use existing operator
		record := operatorRecords[0]
		operator = &pbtypes.SystemOperatorRecord{
			ID:                record.Id,
			Name:              record.GetString("name"),
			PublicKey:         record.GetString("public_key"),
			PrivateKey:        record.GetString("private_key"),
			Seed:              record.GetString("seed"),
			SigningPublicKey:  record.GetString("signing_public_key"),
			SigningPrivateKey: record.GetString("signing_private_key"),
			SigningSeed:       record.GetString("signing_seed"),
			JWT:               record.GetString("jwt"),
		}
	}

	// Check if system account (SYS) exists using proper dbx query
	sysAccountRecords, err := sm.app.FindAllRecords(sm.options.OrganizationCollectionName,
		dbx.HashExp{"account_name": "SYS"})
	if err != nil {
		return fmt.Errorf("failed to find system account records: %w", err)
	}

	if len(sysAccountRecords) == 0 {
		// Create system account
		if err := sm.createSystemAccount(operator); err != nil {
			return fmt.Errorf("failed to create system account: %w", err)
		}
	}

	return nil
}

// createSystemOperator creates the system operator
func (sm *Manager) createSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	// Generate operator keys
	seed, public, signingKey, signingPublic, err := sm.nkeyManager.GenerateOperatorKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator keys: %w", err)
	}

	// Get private key
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key: %w", err)
	}

	// Get signing private key
	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get signing private key: %w", err)
	}

	// Create operator record
	operator := &pbtypes.SystemOperatorRecord{
		Name:              sm.options.OperatorName,
		PublicKey:         public,
		PrivateKey:        privateKey,
		Seed:              seed,
		SigningPublicKey:  signingPublic,
		SigningPrivateKey: signingPrivateKey,
		SigningSeed:       signingKey,
	}

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateOperatorJWT(operator)
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator JWT: %w", err)
	}
	operator.JWT = jwtValue

	// Save to database
	collection, err := sm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, fmt.Errorf("failed to find system operator collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("name", operator.Name)
	record.Set("public_key", operator.PublicKey)
	record.Set("private_key", operator.PrivateKey)
	record.Set("seed", operator.Seed)
	record.Set("signing_public_key", operator.SigningPublicKey)
	record.Set("signing_private_key", operator.SigningPrivateKey)
	record.Set("signing_seed", operator.SigningSeed)
	record.Set("jwt", operator.JWT)

	if err := sm.app.Save(record); err != nil {
		return nil, fmt.Errorf("failed to save system operator: %w", err)
	}

	operator.ID = record.Id

	if sm.options.LogToConsole {
		log.Printf("Created system operator: %s", operator.Name)
	}

	return operator, nil
}

// createSystemAccount creates the system account (SYS) and system user
func (sm *Manager) createSystemAccount(operator *pbtypes.SystemOperatorRecord) error {
	// Generate account keys
	seed, public, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate account keys: %w", err)
	}

	// Get private keys
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return fmt.Errorf("failed to get signing private key: %w", err)
	}

	// Create organization record for system account
	sysAccount := &pbtypes.OrganizationRecord{
		Name:              "System Account",
		AccountName:       "SYS",
		Description:       "Automatically created system account for NATS management",
		PublicKey:         public,
		PrivateKey:        privateKey,
		Seed:              seed,
		SigningPublicKey:  signingPublic,
		SigningPrivateKey: signingPrivateKey,
		SigningSeed:       signingKey,
		Active:            true,
	}

	// Generate system account JWT (special handling for SYS account)
	jwtValue, err := sm.jwtGen.GenerateSystemAccountJWT(sysAccount, operator.SigningSeed)
	if err != nil {
		return fmt.Errorf("failed to generate system account JWT: %w", err)
	}
	sysAccount.JWT = jwtValue

	// Save to database
	collection, err := sm.app.FindCollectionByNameOrId(sm.options.OrganizationCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find organizations collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("name", sysAccount.Name)
	record.Set("account_name", sysAccount.AccountName)
	record.Set("description", sysAccount.Description)
	record.Set("public_key", sysAccount.PublicKey)
	record.Set("private_key", sysAccount.PrivateKey)
	record.Set("seed", sysAccount.Seed)
	record.Set("signing_public_key", sysAccount.SigningPublicKey)
	record.Set("signing_private_key", sysAccount.SigningPrivateKey)
	record.Set("signing_seed", sysAccount.SigningSeed)
	record.Set("jwt", sysAccount.JWT)
	record.Set("active", sysAccount.Active)

	if err := sm.app.Save(record); err != nil {
		return fmt.Errorf("failed to save system account: %w", err)
	}

	if sm.options.LogToConsole {
		log.Printf("Created system account: %s", sysAccount.AccountName)
	}

	// Now create the system user within this account
	if err := sm.createSystemUser(record.Id); err != nil {
		return fmt.Errorf("failed to create system user: %w", err)
	}

	// Manually place system account JWT in NATS resolver directory
	if err := sm.publishSystemAccountToResolver(sysAccount); err != nil {
		// Log warning but don't fail - NATS might auto-trust system accounts
		if sm.options.LogToConsole {
			log.Printf("Warning: Failed to publish system account to resolver: %v", err)
		}
	}

	return nil
}

// createSystemUser creates a system user within the system account
func (sm *Manager) createSystemUser(sysAccountID string) error {
	// Check if system user already exists
	existingUsers, err := sm.app.FindAllRecords(sm.options.UserCollectionName,
		dbx.HashExp{
			"organization_id": sysAccountID,
			"nats_username":   "sys",
		})
	if err != nil {
		return fmt.Errorf("failed to check for existing system user: %w", err)
	}

	if len(existingUsers) > 0 {
		if sm.options.LogToConsole {
			log.Printf("System user already exists")
		}
		return nil
	}

	// Create a default role for system user if it doesn't exist
	systemRole, err := sm.createSystemRole()
	if err != nil {
		return fmt.Errorf("failed to create system role: %w", err)
	}

	// Generate user keys
	seed, public, err := sm.nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate system user keys: %w", err)
	}

	// Get private key
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return fmt.Errorf("failed to get system user private key: %w", err)
	}

	// Create system user record
	collection, err := sm.app.FindCollectionByNameOrId(sm.options.UserCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find users collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("email", "sys@system.local")
	record.Set("nats_username", "sys")
	record.Set("description", "Automatically created system user for NATS management")
	record.Set("organization_id", sysAccountID)
	record.Set("role_id", systemRole.Id)
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)
	record.Set("active", true)
	record.Set("verified", true)

	// Generate JWT and creds for system user
	if err := sm.generateUserJWT(record); err != nil {
		return fmt.Errorf("failed to generate system user JWT: %w", err)
	}

	// Save system user
	if err := sm.app.Save(record); err != nil {
		return fmt.Errorf("failed to save system user: %w", err)
	}

	if sm.options.LogToConsole {
		log.Printf("Created system user: sys")
	}

	return nil
}

// createSystemRole creates a system role with full permissions
func (sm *Manager) createSystemRole() (*core.Record, error) {
	// Check if system role already exists
	existingRoles, err := sm.app.FindAllRecords(sm.options.RoleCollectionName,
		dbx.HashExp{"name": "System Administrator"})
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing system role: %w", err)
	}

	if len(existingRoles) > 0 {
		return existingRoles[0], nil
	}

	// Create system role with full permissions
	collection, err := sm.app.FindCollectionByNameOrId(sm.options.RoleCollectionName)
	if err != nil {
		return nil, fmt.Errorf("failed to find roles collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("name", "System Administrator")
	record.Set("description", "Full system access for NATS management")
	
	// System role gets full access to everything
	publishPerms := []string{"$SYS.>", ">"}  // Full system and global access
	subscribePerms := []string{"$SYS.>", ">"}
	
	publishJSON, _ := json.Marshal(publishPerms)
	subscribeJSON, _ := json.Marshal(subscribePerms)
	
	record.Set("publish_permissions", string(publishJSON))
	record.Set("subscribe_permissions", string(subscribeJSON))
	record.Set("max_connections", -1)  // Unlimited
	record.Set("max_data", -1)         // Unlimited
	record.Set("max_payload", -1)      // Unlimited

	if err := sm.app.Save(record); err != nil {
		return nil, fmt.Errorf("failed to save system role: %w", err)
	}

	if sm.options.LogToConsole {
		log.Printf("Created system role: System Administrator")
	}

	return record, nil
}

// publishSystemAccountToResolver manually publishes the system account JWT
func (sm *Manager) publishSystemAccountToResolver(sysAccount *pbtypes.OrganizationRecord) error {
	// This would ideally write the JWT to the NATS resolver directory
	// For now, we'll just log it so you can manually add it if needed
	if sm.options.LogToConsole {
		log.Printf("System Account JWT: %s", sysAccount.JWT)
		log.Printf("System Account Public Key: %s", sysAccount.PublicKey)
		log.Printf("You may need to place this JWT in your NATS resolver directory as: %s.jwt", sysAccount.PublicKey)
	}
	return nil
}

// setupOrganizationHooks sets up hooks for organization changes
func (sm *Manager) setupOrganizationHooks() {
	// Organization creation
	sm.app.OnRecordCreateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		// Only handle organization collection
		if e.Collection.Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		// Generate keys and JWT before creation
		if err := sm.generateOrganizationKeys(e.Record); err != nil {
			return fmt.Errorf("failed to generate organization keys: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle organization collection
		if e.Record.Collection().Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.OrganizationCollectionName, pbtypes.EventTypeOrgCreate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Organization updates
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle organization collection
		if e.Record.Collection().Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		// Skip system account
		if e.Record.GetString("account_name") == "SYS" {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.OrganizationCollectionName, pbtypes.EventTypeOrgUpdate) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionUpsert)
		}
		return e.Next()
	})

	// Organization deletion
	sm.app.OnRecordDeleteRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		// Only handle organization collection
		if e.Collection.Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		// Prevent deletion of system account
		if e.Record.GetString("account_name") == "SYS" {
			return fmt.Errorf("cannot delete system account")
		}

		if sm.shouldHandleEvent(sm.options.OrganizationCollectionName, pbtypes.EventTypeOrgDelete) {
			sm.scheduleSync(e.Record.Id, pbtypes.PublishActionDelete)
		}
		return e.Next()
	})
}

// setupUserHooks sets up hooks for user changes
func (sm *Manager) setupUserHooks() {
	// User creation
	sm.app.OnRecordCreateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		// Only handle user collection
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// Generate user keys and JWT before creation
		if err := sm.generateUserKeys(e.Record); err != nil {
			return fmt.Errorf("failed to generate user keys: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle user collection
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserCreate) {
			// User changes trigger organization JWT regeneration
			orgID := e.Record.GetString("organization_id")
			if orgID != "" {
				sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		// Only handle user collection
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// Check if role or organization changed
		// Note: We'll regenerate JWT on any update to be safe since we don't have OriginalCopy
		if err := sm.regenerateUserJWT(e.Record); err != nil {
			return fmt.Errorf("failed to regenerate user JWT: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle user collection
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserUpdate) {
			// User changes trigger organization JWT regeneration
			orgID := e.Record.GetString("organization_id")
			if orgID != "" {
				sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User deletion
	sm.app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle user collection
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserDelete) {
			// User deletion triggers organization JWT regeneration (to revoke user)
			orgID := e.Record.GetString("organization_id")
			if orgID != "" {
				sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})
}

// setupRoleHooks sets up hooks for role changes
func (sm *Manager) setupRoleHooks() {
	// Role updates affect all users with that role
	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Only handle role collection
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.RoleCollectionName, pbtypes.EventTypeRoleUpdate) {
			// Find all users with this role and regenerate their JWTs
			if err := sm.regenerateUsersWithRole(e.Record.Id); err != nil {
				log.Printf("Failed to regenerate users with role %s: %v", e.Record.Id, err)
			}
			
			// Trigger publish for all affected organizations
			if err := sm.scheduleOrganizationsWithRole(e.Record.Id); err != nil {
				log.Printf("Failed to schedule organizations with role %s: %v", e.Record.Id, err)
			}
		}
		return e.Next()
	})
}

// shouldHandleEvent determines if an event should be handled
func (sm *Manager) shouldHandleEvent(collectionName, eventType string) bool {
	if sm.options.EventFilter != nil {
		return sm.options.EventFilter(collectionName, eventType)
	}
	return true
}

// scheduleSync schedules a sync operation with debouncing
func (sm *Manager) scheduleSync(orgID, action string) {
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	// Cancel any existing timer
	if sm.timer != nil {
		sm.timer.Stop()
	}

	// Queue the operation immediately
	if err := sm.publisher.QueueAccountUpdate(orgID, action); err != nil && sm.options.LogToConsole {
		log.Printf("Failed to queue account update: %v", err)
	}

	// Schedule processing after debounce interval
	sm.timer = time.AfterFunc(sm.options.DebounceInterval, func() {
		if err := sm.publisher.ProcessPublishQueue(); err != nil && sm.options.LogToConsole {
			log.Printf("Error processing publish queue: %v", err)
		}
	})
}

// generateOrganizationKeys generates keys and JWT for an organization
func (sm *Manager) generateOrganizationKeys(record *core.Record) error {
	// Skip if keys already exist
	if record.GetString("public_key") != "" {
		return nil
	}

	// Generate account keys
	seed, public, signingKey, signingPublic, err := sm.nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return err
	}

	// Get private keys
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return err
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return err
	}

	// Set the keys
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)
	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	// Normalize account name
	if record.GetString("account_name") == "" {
		orgRecord := &pbtypes.OrganizationRecord{Name: record.GetString("name")}
		record.Set("account_name", orgRecord.NormalizeAccountName())
	}

	// Generate JWT
	return sm.generateOrganizationJWT(record)
}

// generateOrganizationJWT generates JWT for an organization
func (sm *Manager) generateOrganizationJWT(record *core.Record) error {
	// Get system operator
	operator, err := sm.getSystemOperator()
	if err != nil {
		return err
	}

	// Create organization record
	org := &pbtypes.OrganizationRecord{
		PublicKey:        record.GetString("public_key"),
		SigningPublicKey: record.GetString("signing_public_key"),
		Name:             record.GetString("name"),
		AccountName:      record.GetString("account_name"),
	}

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateAccountJWT(org, operator.SigningSeed)
	if err != nil {
		return err
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
		return err
	}

	// Get private key
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return err
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
	// Get organization and role
	org, err := sm.app.FindRecordById(sm.options.OrganizationCollectionName, record.GetString("organization_id"))
	if err != nil {
		return fmt.Errorf("failed to find organization: %w", err)
	}

	role, err := sm.app.FindRecordById(sm.options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return fmt.Errorf("failed to find role: %w", err)
	}

	// Convert to models
	user := sm.recordToUserModel(record)
	orgModel := sm.recordToOrgModel(org)
	roleModel := sm.recordToRoleModel(role)

	// Generate JWT
	jwtValue, err := sm.jwtGen.GenerateUserJWT(user, orgModel, roleModel)
	if err != nil {
		return fmt.Errorf("failed to generate user JWT: %w", err)
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	// Generate creds file
	credsFile, err := sm.jwtGen.GenerateCredsFile(user)
	if err != nil {
		return fmt.Errorf("failed to generate creds file: %w", err)
	}

	record.Set("creds_file", credsFile)
	return nil
}

// regenerateUserJWT regenerates JWT for a user (when role/org changes)
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
		ID:             record.Id,
		NatsUsername:   record.GetString("nats_username"),
		PublicKey:      record.GetString("public_key"),
		Seed:           record.GetString("seed"),
		OrganizationID: record.GetString("organization_id"),
		RoleID:         record.GetString("role_id"),
		JWT:            record.GetString("jwt"),
		BearerToken:    record.GetBool("bearer_token"),
		Active:         record.GetBool("active"),
	}
}

func (sm *Manager) recordToOrgModel(record *core.Record) *pbtypes.OrganizationRecord {
	return &pbtypes.OrganizationRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		AccountName:      record.GetString("account_name"),
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

// getSystemOperator gets the system operator
func (sm *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := sm.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, err
	}
	
	if len(records) == 0 {
		return nil, fmt.Errorf("system operator not found")
	}

	record := records[0]
	return &pbtypes.SystemOperatorRecord{
		ID:                record.Id,
		Name:              record.GetString("name"),
		PublicKey:         record.GetString("public_key"),
		SigningSeed:       record.GetString("signing_seed"),
	}, nil
}

// regenerateUsersWithRole regenerates JWTs for all users with a specific role
func (sm *Manager) regenerateUsersWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return err
	}

	for _, user := range users {
		if err := sm.regenerateUserJWT(user); err != nil {
			log.Printf("Failed to regenerate JWT for user %s: %v", user.Id, err)
			continue
		}
		
		if err := sm.app.Save(user); err != nil {
			log.Printf("Failed to save user %s: %v", user.Id, err)
		}
	}

	return nil
}

// scheduleOrganizationsWithRole schedules sync for all organizations that have users with a specific role
func (sm *Manager) scheduleOrganizationsWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return err
	}

	// Collect unique organization IDs
	orgIDs := make(map[string]bool)
	for _, user := range users {
		orgID := user.GetString("organization_id")
		if orgID != "" {
			orgIDs[orgID] = true
		}
	}

	// Schedule sync for each organization
	for orgID := range orgIDs {
		sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
	}

	return nil
}
