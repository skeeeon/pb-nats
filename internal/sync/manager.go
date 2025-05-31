// Package sync handles synchronization between PocketBase and NATS
package sync

import (
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

// SetupHooks sets up all the PocketBase hooks for synchronization
// This is separated from system component initialization to ensure proper order
func (sm *Manager) SetupHooks() error {
	if sm.options.LogToConsole {
		log.Printf("Setting up PocketBase hooks for NATS sync...")
	}

	// Setup hooks for each collection type
	sm.setupOrganizationHooks()
	sm.setupUserHooks()
	sm.setupRoleHooks()

	if sm.options.LogToConsole {
		log.Printf("✅ PocketBase hooks configured for NATS sync")
	}

	return nil
}

// setupOrganizationHooks sets up hooks for organization changes
func (sm *Manager) setupOrganizationHooks() {
	// Organization creation
	sm.app.OnRecordCreateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		if err := sm.generateOrganizationKeys(e.Record); err != nil {
			return fmt.Errorf("failed to generate organization keys: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
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
		if e.Record.Collection().Name != sm.options.OrganizationCollectionName {
			return e.Next()
		}

		// Skip system account to prevent infinite loops
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
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if err := sm.generateUserKeys(e.Record); err != nil {
			return fmt.Errorf("failed to generate user keys: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserCreate) {
			orgID := e.Record.GetString("organization_id")
			if orgID != "" {
				sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
			}
		}
		return e.Next()
	})

	// User updates
	sm.app.OnRecordUpdateRequest().BindFunc(func(e *core.RecordRequestEvent) error {
		if e.Collection.Name != sm.options.UserCollectionName {
			return e.Next()
		}

		// Regenerate JWT on any update to ensure consistency
		if err := sm.regenerateUserJWT(e.Record); err != nil {
			return fmt.Errorf("failed to regenerate user JWT: %w", err)
		}
		return e.Next()
	})

	sm.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.UserCollectionName, pbtypes.EventTypeUserUpdate) {
			orgID := e.Record.GetString("organization_id")
			if orgID != "" {
				sm.scheduleSync(orgID, pbtypes.PublishActionUpsert)
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
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}

		if sm.shouldHandleEvent(sm.options.RoleCollectionName, pbtypes.EventTypeRoleUpdate) {
			// Find all users with this role and regenerate their JWTs
			if err := sm.regenerateUsersWithRole(e.Record.Id); err != nil {
				if sm.options.LogToConsole {
					log.Printf("⚠️  Failed to regenerate users with role %s: %v", e.Record.Id, err)
				}
			}
			
			// Trigger publish for all affected organizations
			if err := sm.scheduleOrganizationsWithRole(e.Record.Id); err != nil {
				if sm.options.LogToConsole {
					log.Printf("⚠️  Failed to schedule organizations with role %s: %v", e.Record.Id, err)
				}
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
func (sm *Manager) scheduleSync(orgID, action string) {
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	// Cancel any existing timer
	if sm.timer != nil {
		sm.timer.Stop()
	}

	// Queue the operation immediately
	if err := sm.publisher.QueueAccountUpdate(orgID, action); err != nil {
		if sm.options.LogToConsole {
			log.Printf("⚠️  Failed to queue account update for org %s: %v", orgID, err)
		}
	}

	// Schedule processing after debounce interval
	sm.timer = time.AfterFunc(sm.options.DebounceInterval, func() {
		if err := sm.publisher.ProcessPublishQueue(); err != nil {
			if sm.options.LogToConsole {
				log.Printf("⚠️  Error processing publish queue: %v", err)
			}
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
		return fmt.Errorf("failed to generate account key pair: %w", err)
	}

	// Get private keys
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return fmt.Errorf("failed to get private key from seed: %w", err)
	}

	signingPrivateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return fmt.Errorf("failed to get signing private key from seed: %w", err)
	}

	// Set the keys
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)
	record.Set("signing_public_key", signingPublic)
	record.Set("signing_private_key", signingPrivateKey)
	record.Set("signing_seed", signingKey)

	// Normalize account name if not set
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
		return fmt.Errorf("failed to get system operator: %w", err)
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
		return fmt.Errorf("failed to generate account JWT: %w", err)
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
		return fmt.Errorf("failed to generate user key pair: %w", err)
	}

	// Get private key
	privateKey, err := sm.nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return fmt.Errorf("failed to get private key from seed: %w", err)
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
		return fmt.Errorf("failed to find organization %s: %w", record.GetString("organization_id"), err)
	}

	role, err := sm.app.FindRecordById(sm.options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return fmt.Errorf("failed to find role %s: %w", record.GetString("role_id"), err)
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

// getSystemOperator gets the system operator with consistent error handling
func (sm *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := sm.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, fmt.Errorf("failed to find system operator records: %w", err)
	}
	
	if len(records) == 0 {
		return nil, fmt.Errorf("system operator not found - ensure Setup() completed successfully")
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
		return fmt.Errorf("failed to find users with role %s: %w", roleID, err)
	}

	for _, user := range users {
		if err := sm.regenerateUserJWT(user); err != nil {
			if sm.options.LogToConsole {
				log.Printf("⚠️  Failed to regenerate JWT for user %s: %v", user.Id, err)
			}
			continue
		}
		
		if err := sm.app.Save(user); err != nil {
			if sm.options.LogToConsole {
				log.Printf("⚠️  Failed to save user %s: %v", user.Id, err)
			}
		}
	}

	return nil
}

// scheduleOrganizationsWithRole schedules sync for all organizations that have users with a specific role
func (sm *Manager) scheduleOrganizationsWithRole(roleID string) error {
	users, err := sm.app.FindAllRecords(sm.options.UserCollectionName, dbx.HashExp{"role_id": roleID})
	if err != nil {
		return fmt.Errorf("failed to find users with role %s: %w", roleID, err)
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
