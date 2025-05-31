// Package pbnats provides seamless integration between PocketBase and NATS server
// by automatically generating and managing NATS JWT authentication.
package pbnats

import (
	"fmt"
	"log"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/collections"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	"github.com/skeeeon/pb-nats/internal/publisher"
	"github.com/skeeeon/pb-nats/internal/sync"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Re-export types for external use
type Options = pbtypes.Options
type OrganizationRecord = pbtypes.OrganizationRecord
type NatsUserRecord = pbtypes.NatsUserRecord
type RoleRecord = pbtypes.RoleRecord
type SystemOperatorRecord = pbtypes.SystemOperatorRecord
type PublishQueueRecord = pbtypes.PublishQueueRecord

// Re-export constants for external use  
const (
	DefaultOrganizationCollectionName = pbtypes.DefaultOrganizationCollectionName
	DefaultUserCollectionName         = pbtypes.DefaultUserCollectionName
	DefaultRoleCollectionName         = pbtypes.DefaultRoleCollectionName
	SystemOperatorCollectionName      = pbtypes.SystemOperatorCollectionName
	PublishQueueCollectionName        = pbtypes.PublishQueueCollectionName
	
	DefaultOperatorName = pbtypes.DefaultOperatorName
	
	PublishActionUpsert = pbtypes.PublishActionUpsert
	PublishActionDelete = pbtypes.PublishActionDelete
	
	EventTypeOrgCreate    = pbtypes.EventTypeOrgCreate
	EventTypeOrgUpdate    = pbtypes.EventTypeOrgUpdate
	EventTypeOrgDelete    = pbtypes.EventTypeOrgDelete
	EventTypeUserCreate   = pbtypes.EventTypeUserCreate
	EventTypeUserUpdate   = pbtypes.EventTypeUserUpdate
	EventTypeUserDelete   = pbtypes.EventTypeUserDelete
	EventTypeRoleCreate   = pbtypes.EventTypeRoleCreate
	EventTypeRoleUpdate   = pbtypes.EventTypeRoleUpdate
	EventTypeRoleDelete   = pbtypes.EventTypeRoleDelete
	
	DefaultOrgPublish      = pbtypes.DefaultOrgPublish
	DefaultUserPublish     = pbtypes.DefaultUserPublish
	DefaultInboxSubscribe  = pbtypes.DefaultInboxSubscribe
)

// Re-export variables for external use
var (
	DefaultOrgSubscribe  = pbtypes.DefaultOrgSubscribe
	DefaultUserSubscribe = pbtypes.DefaultUserSubscribe
)

// Re-export utility functions
var (
	ApplyOrganizationScope = pbtypes.ApplyOrganizationScope
	ApplyUserScope         = pbtypes.ApplyUserScope
)

// Setup initializes the NATS JWT synchronization for a PocketBase instance.
// This is the main entry point for the library.
//
// Example usage:
//
//	app := pocketbase.New()
//	if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
//	    log.Fatalf("Failed to setup NATS sync: %v", err)
//	}
//	app.Start()
func Setup(app *pocketbase.PocketBase, options Options) error {
	// Apply default options for any missing values
	options = applyDefaultOptions(options)

	// Validate required options
	if err := validateOptions(options); err != nil {
		return fmt.Errorf("invalid options: %w", err)
	}

	// Initialize all components after the app is bootstrapped
	// This ensures proper initialization order and prevents race conditions
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		// Wait for bootstrap to complete first
		if err := e.Next(); err != nil {
			return fmt.Errorf("bootstrap failed: %w", err)
		}

		// Initialize components in correct order to prevent race conditions
		if err := initializeComponents(app, options); err != nil {
			if options.LogToConsole {
				log.Printf("❌ NATS sync initialization failed: %v", err)
			}
			return fmt.Errorf("failed to initialize NATS sync: %w", err)
		}

		if options.LogToConsole {
			log.Printf("✅ PocketBase NATS JWT sync initialized successfully")
		}

		return nil
	})

	if options.LogToConsole {
		log.Printf("🚀 PocketBase NATS JWT sync scheduled for initialization")
		log.Printf("   User collection: %s", options.UserCollectionName)
		log.Printf("   Role collection: %s", options.RoleCollectionName)
		log.Printf("   Organization collection: %s", options.OrganizationCollectionName)
		log.Printf("   NATS server: %s", options.NATSServerURL)
		log.Printf("   Operator: %s", options.OperatorName)
	}

	return nil
}

// initializeComponents initializes all NATS sync components in the correct order
// to prevent race conditions and ensure proper dependency resolution
func initializeComponents(app *pocketbase.PocketBase, options Options) error {
	if options.LogToConsole {
		log.Printf("🔧 Initializing NATS sync components...")
	}

	// Step 1: Initialize collections first - must happen before anything else
	if options.LogToConsole {
		log.Printf("   Creating collections...")
	}
	collectionManager := collections.NewManager(app, options)
	if err := collectionManager.InitializeCollections(); err != nil {
		return fmt.Errorf("failed to initialize collections: %w", err)
	}
	if options.LogToConsole {
		log.Printf("   ✅ Collections initialized")
	}

	// Step 2: Initialize NKey manager - no dependencies
	nkeyManager := nkey.NewManager()
	if options.LogToConsole {
		log.Printf("   ✅ NKey manager initialized")
	}

	// Step 3: Initialize JWT generator - depends on NKey manager
	jwtGenerator := jwt.NewGenerator(app, nkeyManager, options)
	if options.LogToConsole {
		log.Printf("   ✅ JWT generator initialized")
	}

	// Step 4: Initialize system components - depends on collections and JWT generator
	// This must happen before publisher starts to avoid processing incomplete data
	if options.LogToConsole {
		log.Printf("   Creating system components...")
	}
	if err := initializeSystemComponents(app, jwtGenerator, nkeyManager, options); err != nil {
		return fmt.Errorf("failed to initialize system components: %w", err)
	}
	if options.LogToConsole {
		log.Printf("   ✅ System components initialized")
	}

	// Step 5: Initialize publisher - depends on system components being ready
	if options.LogToConsole {
		log.Printf("   Starting account publisher...")
	}
	accountPublisher := publisher.NewManager(app, options)
	if err := accountPublisher.Start(); err != nil {
		return fmt.Errorf("failed to start account publisher: %w", err)
	}
	if options.LogToConsole {
		log.Printf("   ✅ Account publisher started")
	}

	// Step 6: Initialize sync manager - sets up hooks, depends on all other components
	if options.LogToConsole {
		log.Printf("   Setting up sync hooks...")
	}
	syncManager := sync.NewManager(app, jwtGenerator, nkeyManager, accountPublisher, options)
	if err := syncManager.SetupHooks(); err != nil {
		return fmt.Errorf("failed to setup sync hooks: %w", err)
	}
	if options.LogToConsole {
		log.Printf("   ✅ Sync hooks configured")
	}

	// Step 7: Setup cleanup handlers
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// Setup graceful shutdown when needed
		return e.Next()
	})

	if options.LogToConsole {
		log.Printf("🎉 NATS JWT sync fully initialized and ready")
		log.Printf("   System operator: %s", options.OperatorName)
		log.Printf("   Queue processing: %v intervals", options.PublishQueueInterval)
		log.Printf("   Debounce delay: %v", options.DebounceInterval)
	}

	return nil
}

// initializeSystemComponents creates system operator, account, role, and user
// This is the proven working implementation adapted from the original sync/manager.go
func initializeSystemComponents(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, options Options) error {
	// Check if system operator exists
	operatorRecords, err := app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find system operator records: %w", err)
	}

	var operator *pbtypes.SystemOperatorRecord
	var sysAccountID string
	var sysAccountPubKey string

	if len(operatorRecords) == 0 {
		// **WORKING INITIALIZATION FLOW** (adapted from original):
		// 1. Create operator (without JWT initially)
		// 2. Create system account 
		// 3. Update operator JWT with system account reference
		// 4. Create system role and user

		// Step 1: Create system operator without JWT
		operator, err = createSystemOperatorWithoutJWT(app, nkeyManager, options)
		if err != nil {
			return fmt.Errorf("failed to create system operator: %w", err)
		}

		// Step 2: Create system account
		sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options)
		if err != nil {
			return fmt.Errorf("failed to create system account: %w", err)
		}

		// Step 3: Update operator JWT with system account reference
		if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey); err != nil {
			return fmt.Errorf("failed to update operator JWT: %w", err)
		}

		if options.LogToConsole {
			log.Printf("✅ Created and configured system operator with system account reference")
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

		// Check if system account exists
		sysAccountRecords, err := app.FindAllRecords(options.OrganizationCollectionName,
			dbx.HashExp{"account_name": "SYS"})
		if err != nil {
			return fmt.Errorf("failed to find system account records: %w", err)
		}

		if len(sysAccountRecords) == 0 {
			// Create system account with existing operator
			sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options)
			if err != nil {
				return fmt.Errorf("failed to create system account: %w", err)
			}

			// Update operator JWT with system account reference
			if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey); err != nil {
				return fmt.Errorf("failed to update operator JWT: %w", err)
			}
		} else {
			sysAccountID = sysAccountRecords[0].Id
			sysAccountPubKey = sysAccountRecords[0].GetString("public_key")
		}
	}

	// Check if system role exists
	sysRoleRecords, err := app.FindAllRecords(options.RoleCollectionName,
		dbx.HashExp{"name": "system_admin"})
	if err != nil {
		return fmt.Errorf("failed to find system role records: %w", err)
	}

	var sysRoleID string
	if len(sysRoleRecords) == 0 {
		// Create system role
		sysRoleID, err = createSystemRole(app, options)
		if err != nil {
			return fmt.Errorf("failed to create system role: %w", err)
		}
	} else {
		sysRoleID = sysRoleRecords[0].Id
	}

	// Check if system user exists
	sysUserRecords, err := app.FindAllRecords(options.UserCollectionName,
		dbx.HashExp{"nats_username": "sys", "organization_id": sysAccountID})
	if err != nil {
		return fmt.Errorf("failed to find system user records: %w", err)
	}

	if len(sysUserRecords) == 0 {
		// Create system user
		if err := createSystemUser(app, jwtGen, nkeyManager, sysAccountID, sysRoleID, options); err != nil {
			return fmt.Errorf("failed to create system user: %w", err)
		}
	}

	return nil
}

// createSystemOperatorWithoutJWT creates the system operator without generating JWT initially
// This is the proven working implementation from the original code
func createSystemOperatorWithoutJWT(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options Options) (*pbtypes.SystemOperatorRecord, error) {
	// Generate operator keys
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateOperatorKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator keys: %w", err)
	}

	// Get private key
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key: %w", err)
	}

	// Get signing private key
	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get signing private key: %w", err)
	}

	// Create operator record
	operator := &pbtypes.SystemOperatorRecord{
		Name:              options.OperatorName,
		PublicKey:         public,
		PrivateKey:        privateKey,
		Seed:              seed,
		SigningPublicKey:  signingPublic,
		SigningPrivateKey: signingPrivateKey,
		SigningSeed:       signingKey,
		JWT:               "", // Will be generated later
	}

	// Save to database without JWT initially
	collection, err := app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
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
	record.Set("jwt", "") // Empty initially

	if err := app.Save(record); err != nil {
		return nil, fmt.Errorf("failed to save system operator: %w", err)
	}

	operator.ID = record.Id

	if options.LogToConsole {
		log.Printf("Created system operator (without JWT): %s", operator.Name)
	}

	return operator, nil
}

// updateOperatorJWT updates the operator JWT with system account reference
// This is the proven working implementation from the original code
func updateOperatorJWT(app *pocketbase.PocketBase, jwtGen *jwt.Generator, operatorID, systemAccountPubKey string) error {
	// Get the operator record
	operatorRecord, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, operatorID)
	if err != nil {
		return fmt.Errorf("failed to find operator record: %w", err)
	}

	// Create operator model
	operator := &pbtypes.SystemOperatorRecord{
		ID:                operatorRecord.Id,
		Name:              operatorRecord.GetString("name"),
		PublicKey:         operatorRecord.GetString("public_key"),
		PrivateKey:        operatorRecord.GetString("private_key"),
		Seed:              operatorRecord.GetString("seed"),
		SigningPublicKey:  operatorRecord.GetString("signing_public_key"),
		SigningPrivateKey: operatorRecord.GetString("signing_private_key"),
		SigningSeed:       operatorRecord.GetString("signing_seed"),
	}

	// Generate JWT with system account reference
	jwtValue, err := jwtGen.GenerateOperatorJWT(operator, systemAccountPubKey)
	if err != nil {
		return fmt.Errorf("failed to generate operator JWT: %w", err)
	}

	// Update the record
	operatorRecord.Set("jwt", jwtValue)
	if err := app.Save(operatorRecord); err != nil {
		return fmt.Errorf("failed to save updated operator JWT: %w", err)
	}

	return nil
}

// createSystemAccount creates the system account (SYS)
// This is the proven working implementation adapted from the original code
func createSystemAccount(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, operator *pbtypes.SystemOperatorRecord, options Options) (string, string, error) {
	// Generate account keys
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate account keys: %w", err)
	}

	// Get private keys
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return "", "", fmt.Errorf("failed to get private key: %w", err)
	}

	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to get signing private key: %w", err)
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
	jwtValue, err := jwtGen.GenerateSystemAccountJWT(sysAccount, operator.SigningSeed)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate system account JWT: %w", err)
	}
	sysAccount.JWT = jwtValue

	// Save to database
	collection, err := app.FindCollectionByNameOrId(options.OrganizationCollectionName)
	if err != nil {
		return "", "", fmt.Errorf("failed to find organizations collection: %w", err)
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

	if err := app.Save(record); err != nil {
		return "", "", fmt.Errorf("failed to save system account: %w", err)
	}

	if options.LogToConsole {
		log.Printf("Created system account: %s (Public Key: %s)", sysAccount.AccountName, sysAccount.PublicKey)
	}

	return record.Id, sysAccount.PublicKey, nil
}

// createSystemRole creates the system role for system users
func createSystemRole(app *pocketbase.PocketBase, options Options) (string, error) {
	collection, err := app.FindCollectionByNameOrId(options.RoleCollectionName)
	if err != nil {
		return "", fmt.Errorf("failed to find roles collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("name", "system_admin")
	record.Set("description", "System administrator role with full NATS access")
	record.Set("publish_permissions", `["$SYS.>", ">"]`)  // Full system and global access
	record.Set("subscribe_permissions", `["$SYS.>", ">"]`) // Full system and global access
	record.Set("is_default", false)
	record.Set("max_connections", -1) // Unlimited
	record.Set("max_data", -1)        // Unlimited
	record.Set("max_payload", -1)     // Unlimited

	if err := app.Save(record); err != nil {
		return "", fmt.Errorf("failed to save system role: %w", err)
	}

	if options.LogToConsole {
		log.Printf("Created system role: system_admin")
	}

	return record.Id, nil
}

// createSystemUser creates the system user for NATS connections
func createSystemUser(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, sysAccountID, sysRoleID string, options Options) error {
	collection, err := app.FindCollectionByNameOrId(options.UserCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find users collection: %w", err)
	}

	record := core.NewRecord(collection)
	
	// PocketBase auth fields
	record.Set("email", "system@localhost.com")
	record.Set("password", "system-generated-password-"+time.Now().Format("20060102150405"))
	record.Set("verified", true)
	
	// NATS-specific fields
	record.Set("nats_username", "sys")
	record.Set("description", "System user for NATS management operations")
	record.Set("organization_id", sysAccountID)
	record.Set("role_id", sysRoleID)
	record.Set("bearer_token", false)
	record.Set("active", true)

	// Generate user keys and JWT
	if err := generateUserKeys(app, jwtGen, nkeyManager, record, options); err != nil {
		return fmt.Errorf("failed to generate system user keys: %w", err)
	}

	if err := app.Save(record); err != nil {
		return fmt.Errorf("failed to save system user: %w", err)
	}

	if options.LogToConsole {
		log.Printf("Created system user: sys")
	}

	return nil
}

// generateUserKeys generates keys and JWT for a user
// This is adapted from the working implementation in sync/manager.go
func generateUserKeys(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, record *core.Record, options Options) error {
	// Skip if keys already exist
	if record.GetString("public_key") != "" {
		return nil
	}

	// Generate user keys
	seed, public, err := nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate user key pair: %w", err)
	}

	// Get private key
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return fmt.Errorf("failed to get private key from seed: %w", err)
	}

	// Set the keys
	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)

	// Generate JWT and creds file
	return generateUserJWT(app, jwtGen, record, options)
}

// generateUserJWT generates JWT and creds file for a user
// This is adapted from the working implementation in sync/manager.go
func generateUserJWT(app *pocketbase.PocketBase, jwtGen *jwt.Generator, record *core.Record, options Options) error {
	// Get organization and role
	org, err := app.FindRecordById(options.OrganizationCollectionName, record.GetString("organization_id"))
	if err != nil {
		return fmt.Errorf("failed to find organization %s: %w", record.GetString("organization_id"), err)
	}

	role, err := app.FindRecordById(options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return fmt.Errorf("failed to find role %s: %w", record.GetString("role_id"), err)
	}

	// Convert to models
	user := recordToUserModel(record)
	orgModel := recordToOrgModel(org)
	roleModel := recordToRoleModel(role)

	// Generate JWT
	jwtValue, err := jwtGen.GenerateUserJWT(user, orgModel, roleModel)
	if err != nil {
		return fmt.Errorf("failed to generate user JWT: %w", err)
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	// Generate creds file
	credsFile, err := jwtGen.GenerateCredsFile(user)
	if err != nil {
		return fmt.Errorf("failed to generate creds file: %w", err)
	}

	record.Set("creds_file", credsFile)
	return nil
}

// Helper methods to convert records to models (from original sync/manager.go)
func recordToUserModel(record *core.Record) *pbtypes.NatsUserRecord {
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

func recordToOrgModel(record *core.Record) *pbtypes.OrganizationRecord {
	return &pbtypes.OrganizationRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		AccountName:      record.GetString("account_name"),
		PublicKey:        record.GetString("public_key"),
		SigningSeed:      record.GetString("signing_seed"),
		JWT:              record.GetString("jwt"),
	}
}

func recordToRoleModel(record *core.Record) *pbtypes.RoleRecord {
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

// validateOptions validates the provided options with consistent error handling
func validateOptions(options Options) error {
	if options.UserCollectionName == "" {
		return fmt.Errorf("user collection name cannot be empty")
	}
	
	if options.RoleCollectionName == "" {
		return fmt.Errorf("role collection name cannot be empty")
	}
	
	if options.OrganizationCollectionName == "" {
		return fmt.Errorf("organization collection name cannot be empty")
	}
	
	if options.OperatorName == "" {
		return fmt.Errorf("operator name cannot be empty")
	}
	
	if options.NATSServerURL == "" {
		return fmt.Errorf("NATS server URL cannot be empty")
	}
	
	if options.PublishQueueInterval <= 0 {
		return fmt.Errorf("publish queue interval must be positive, got: %v", options.PublishQueueInterval)
	}
	
	if options.DebounceInterval <= 0 {
		return fmt.Errorf("debounce interval must be positive, got: %v", options.DebounceInterval)
	}

	return nil
}

// GetDefaultOperatorName returns the default operator name
func GetDefaultOperatorName() string {
	return DefaultOperatorName
}

// GetDefaultCollectionNames returns the default collection names
func GetDefaultCollectionNames() (user, role, org string) {
	return DefaultUserCollectionName, DefaultRoleCollectionName, DefaultOrganizationCollectionName
}

// Version information
const Version = "1.0.0"

// GetVersion returns the library version
func GetVersion() string {
	return Version
}
