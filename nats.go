// Package pbnats provides seamless integration between PocketBase and NATS server
// by automatically generating and managing NATS JWT authentication.
package pbnats

import (
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/collections"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	"github.com/skeeeon/pb-nats/internal/publisher"
	"github.com/skeeeon/pb-nats/internal/sync"
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Re-export types for external use
type Options = pbtypes.Options
type AccountRecord = pbtypes.AccountRecord
type NatsUserRecord = pbtypes.NatsUserRecord
type RoleRecord = pbtypes.RoleRecord
type SystemOperatorRecord = pbtypes.SystemOperatorRecord
type PublishQueueRecord = pbtypes.PublishQueueRecord

// Re-export constants for external use  
const (
	DefaultAccountCollectionName      = pbtypes.DefaultAccountCollectionName
	DefaultUserCollectionName         = pbtypes.DefaultUserCollectionName
	DefaultRoleCollectionName         = pbtypes.DefaultRoleCollectionName
	SystemOperatorCollectionName      = pbtypes.SystemOperatorCollectionName
	PublishQueueCollectionName        = pbtypes.PublishQueueCollectionName
	
	DefaultOperatorName = pbtypes.DefaultOperatorName
	
	PublishActionUpsert = pbtypes.PublishActionUpsert
	PublishActionDelete = pbtypes.PublishActionDelete
	
	EventTypeAccountCreate = pbtypes.EventTypeAccountCreate
	EventTypeAccountUpdate = pbtypes.EventTypeAccountUpdate
	EventTypeAccountDelete = pbtypes.EventTypeAccountDelete
	EventTypeUserCreate    = pbtypes.EventTypeUserCreate
	EventTypeUserUpdate    = pbtypes.EventTypeUserUpdate
	EventTypeUserDelete    = pbtypes.EventTypeUserDelete
	EventTypeRoleCreate    = pbtypes.EventTypeRoleCreate
	EventTypeRoleUpdate    = pbtypes.EventTypeRoleUpdate
	EventTypeRoleDelete    = pbtypes.EventTypeRoleDelete
	
	DefaultInboxSubscribe = pbtypes.DefaultInboxSubscribe
)

// Re-export variables for external use
var (
	DefaultPublishPermissions   = pbtypes.DefaultPublishPermissions
	DefaultSubscribePermissions = pbtypes.DefaultSubscribePermissions
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

	// Create logger for consistent logging
	logger := utils.NewLogger(options.LogToConsole)

	// Validate required options
	if err := validateOptions(options); err != nil {
		return utils.WrapError(err, "invalid options")
	}

	// Initialize all components after the app is bootstrapped
	// This ensures proper initialization order and prevents race conditions
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		// Wait for bootstrap to complete first
		if err := e.Next(); err != nil {
			return utils.WrapError(err, "bootstrap failed")
		}

		// Initialize components in correct order to prevent race conditions
		if err := initializeComponents(app, options, logger); err != nil {
			logger.Error("NATS sync initialization failed: %v", err)
			return utils.WrapError(err, "failed to initialize NATS sync")
		}

		logger.Success("PocketBase NATS JWT sync initialized successfully")

		return nil
	})

	logger.Start("PocketBase NATS JWT sync scheduled for initialization")
	logger.Info("   User collection: %s", options.UserCollectionName)
	logger.Info("   Role collection: %s", options.RoleCollectionName)
	logger.Info("   Account collection: %s", options.AccountCollectionName)
	logger.Info("   NATS server: %s", options.NATSServerURL)
	logger.Info("   Operator: %s", options.OperatorName)

	return nil
}

// initializeComponents initializes all NATS sync components in the correct order
// to prevent race conditions and ensure proper dependency resolution
func initializeComponents(app *pocketbase.PocketBase, options Options, logger *utils.Logger) error {
	logger.Process("Initializing NATS sync components...")

	// Step 1: Initialize collections first - must happen before anything else
	logger.Info("   Creating collections...")
	collectionManager := collections.NewManager(app, options)
	if err := collectionManager.InitializeCollections(); err != nil {
		return utils.WrapError(err, "failed to initialize collections")
	}
	logger.Success("   Collections initialized")

	// Step 2: Initialize NKey manager - no dependencies
	nkeyManager := nkey.NewManager()
	logger.Success("   NKey manager initialized")

	// Step 3: Initialize JWT generator - depends on NKey manager
	jwtGenerator := jwt.NewGenerator(app, nkeyManager, options)
	logger.Success("   JWT generator initialized")

	// Step 4: Initialize system components - depends on collections and JWT generator
	// This must happen before publisher starts to avoid processing incomplete data
	logger.Info("   Creating system components...")
	if err := initializeSystemComponents(app, jwtGenerator, nkeyManager, options, logger); err != nil {
		return utils.WrapError(err, "failed to initialize system components")
	}
	logger.Success("   System components initialized")

	// Step 5: Initialize publisher - depends on system components being ready
	logger.Info("   Starting account publisher...")
	accountPublisher := publisher.NewManager(app, options)
	if err := accountPublisher.Start(); err != nil {
		return utils.WrapError(err, "failed to start account publisher")
	}
	logger.Success("   Account publisher started")

	// Step 6: Initialize sync manager - sets up hooks, depends on all other components
	logger.Info("   Setting up sync hooks...")
	syncManager := sync.NewManager(app, jwtGenerator, nkeyManager, accountPublisher, options)
	if err := syncManager.SetupHooks(); err != nil {
		return utils.WrapError(err, "failed to setup sync hooks")
	}
	logger.Success("   Sync hooks configured")

	logger.Success("NATS JWT sync fully initialized and ready")
	logger.Info("   System operator: %s", options.OperatorName)
	logger.Info("   Queue processing: %v intervals", options.PublishQueueInterval)
	logger.Info("   Debounce delay: %v", options.DebounceInterval)
	logger.Info("   Resource cleanup: automatic via context cancellation")

	return nil
}

// initializeSystemComponents creates system operator, account, role, and user
// This is the proven working implementation adapted from the original sync/manager.go
func initializeSystemComponents(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, options Options, logger *utils.Logger) error {
	// Check if system operator exists
	operatorRecords, err := app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to find system operator records")
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
		operator, err = createSystemOperatorWithoutJWT(app, nkeyManager, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system operator")
		}

		// Step 2: Create system account
		sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system account")
		}

		// Step 3: Update operator JWT with system account reference
		if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey, logger); err != nil {
			return utils.WrapError(err, "failed to update operator JWT")
		}

		logger.Success("Created and configured system operator with system account reference")
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
		sysAccountRecords, err := app.FindAllRecords(options.AccountCollectionName,
			dbx.HashExp{"name": "System Account"})
		if err != nil {
			return utils.WrapError(err, "failed to find system account records")
		}

		if len(sysAccountRecords) == 0 {
			// Create system account with existing operator
			sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options, logger)
			if err != nil {
				return utils.WrapError(err, "failed to create system account")
			}

			// Update operator JWT with system account reference
			if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey, logger); err != nil {
				return utils.WrapError(err, "failed to update operator JWT")
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
		return utils.WrapError(err, "failed to find system role records")
	}

	var sysRoleID string
	if len(sysRoleRecords) == 0 {
		// Create system role
		sysRoleID, err = createSystemRole(app, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system role")
		}
	} else {
		sysRoleID = sysRoleRecords[0].Id
	}

	// Check if system user exists
	sysUserRecords, err := app.FindAllRecords(options.UserCollectionName,
		dbx.HashExp{"nats_username": "sys", "account_id": sysAccountID})
	if err != nil {
		return utils.WrapError(err, "failed to find system user records")
	}

	if len(sysUserRecords) == 0 {
		// Create system user
		if err := createSystemUser(app, jwtGen, nkeyManager, sysAccountID, sysRoleID, options, logger); err != nil {
			return utils.WrapError(err, "failed to create system user")
		}
	}

	return nil
}

// createSystemOperatorWithoutJWT creates the system operator without generating JWT initially
// This is the proven working implementation from the original code
func createSystemOperatorWithoutJWT(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options Options, logger *utils.Logger) (*pbtypes.SystemOperatorRecord, error) {
	// Generate operator keys
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateOperatorKeyPair()
	if err != nil {
		return nil, utils.WrapError(err, "failed to generate operator keys")
	}

	// Get private key
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return nil, utils.WrapError(err, "failed to get private key")
	}

	// Get signing private key
	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return nil, utils.WrapError(err, "failed to get signing private key")
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
		return nil, utils.WrapError(err, "failed to find system operator collection")
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
		return nil, utils.WrapError(err, "failed to save system operator")
	}

	operator.ID = record.Id

	logger.Success("Created system operator (without JWT): %s", operator.Name)

	return operator, nil
}

// updateOperatorJWT updates the operator JWT with system account reference
// This is the proven working implementation from the original code
func updateOperatorJWT(app *pocketbase.PocketBase, jwtGen *jwt.Generator, operatorID, systemAccountPubKey string, logger *utils.Logger) error {
	// Get the operator record
	operatorRecord, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, operatorID)
	if err != nil {
		return utils.WrapError(err, "failed to find operator record")
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
		return utils.WrapError(err, "failed to generate operator JWT")
	}

	// Update the record
	operatorRecord.Set("jwt", jwtValue)
	if err := app.Save(operatorRecord); err != nil {
		return utils.WrapError(err, "failed to save updated operator JWT")
	}

	return nil
}

// createSystemAccount creates the system account (SYS)
// This is the proven working implementation adapted from the original code
func createSystemAccount(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, operator *pbtypes.SystemOperatorRecord, options Options, logger *utils.Logger) (string, string, error) {
	// Generate account keys
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return "", "", utils.WrapError(err, "failed to generate account keys")
	}

	// Get private keys
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to get private key")
	}

	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to get signing private key")
	}

	// Create account record for system account
	sysAccount := &pbtypes.AccountRecord{
		Name:              "System Account",
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
		return "", "", utils.WrapError(err, "failed to generate system account JWT")
	}
	sysAccount.JWT = jwtValue

	// Save to database
	collection, err := app.FindCollectionByNameOrId(options.AccountCollectionName)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to find accounts collection")
	}

	record := core.NewRecord(collection)
	record.Set("name", sysAccount.Name)
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
		return "", "", utils.WrapError(err, "failed to save system account")
	}

	logger.Success("Created system account: %s (Public Key: %s)", sysAccount.NormalizeName(), sysAccount.PublicKey)

	return record.Id, sysAccount.PublicKey, nil
}

// createSystemRole creates the system role for system users
func createSystemRole(app *pocketbase.PocketBase, options Options, logger *utils.Logger) (string, error) {
	collection, err := app.FindCollectionByNameOrId(options.RoleCollectionName)
	if err != nil {
		return "", utils.WrapError(err, "failed to find roles collection")
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
		return "", utils.WrapError(err, "failed to save system role")
	}

	logger.Success("Created system role: system_admin")

	return record.Id, nil
}

// createSystemUser creates the system user for NATS connections
func createSystemUser(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, sysAccountID, sysRoleID string, options Options, logger *utils.Logger) error {
	collection, err := app.FindCollectionByNameOrId(options.UserCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to find users collection")
	}

	record := core.NewRecord(collection)
	
	// PocketBase auth fields
	record.Set("email", "system@localhost.com")
	record.Set("password", "system-generated-password-"+time.Now().Format("20060102150405"))
	record.Set("verified", true)
	
	// NATS-specific fields
	record.Set("nats_username", "sys")
	record.Set("description", "System user for NATS management operations")
	record.Set("account_id", sysAccountID)
	record.Set("role_id", sysRoleID)
	record.Set("bearer_token", false)
	record.Set("regenerate", false)
	record.Set("active", true)

	// Generate user keys and JWT
	if err := generateUserKeys(app, jwtGen, nkeyManager, record, options); err != nil {
		return utils.WrapError(err, "failed to generate system user keys")
	}

	if err := app.Save(record); err != nil {
		return utils.WrapError(err, "failed to save system user")
	}

	logger.Success("Created system user: sys")

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
		return utils.WrapError(err, "failed to generate user key pair")
	}

	// Get private key
	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
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
	// Get account and role
	account, err := app.FindRecordById(options.AccountCollectionName, record.GetString("account_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find account %s", record.GetString("account_id"))
	}

	role, err := app.FindRecordById(options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find role %s", record.GetString("role_id"))
	}

	// Convert to models
	user := recordToUserModel(record)
	accountModel := recordToAccountModel(account)
	roleModel := recordToRoleModel(role)

	// Generate JWT
	jwtValue, err := jwtGen.GenerateUserJWT(user, accountModel, roleModel)
	if err != nil {
		return utils.WrapError(err, "failed to generate user JWT")
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	// Generate creds file
	credsFile, err := jwtGen.GenerateCredsFile(user)
	if err != nil {
		return utils.WrapError(err, "failed to generate creds file")
	}

	record.Set("creds_file", credsFile)
	return nil
}

// Helper methods to convert records to models (from original sync/manager.go)
func recordToUserModel(record *core.Record) *pbtypes.NatsUserRecord {
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

func recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
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

// validateOptions validates the provided options with consistent error handling and enhanced validation
func validateOptions(options Options) error {
	if err := utils.ValidateRequired(options.UserCollectionName, "user collection name"); err != nil {
		return err
	}
	
	if err := utils.ValidateRequired(options.RoleCollectionName, "role collection name"); err != nil {
		return err
	}
	
	if err := utils.ValidateRequired(options.AccountCollectionName, "account collection name"); err != nil {
		return err
	}
	
	if err := utils.ValidateRequired(options.OperatorName, "operator name"); err != nil {
		return err
	}
	
	if err := utils.ValidateURL(options.NATSServerURL, "NATS server URL"); err != nil {
		return err
	}
	
	// Validate backup URLs if provided
	for i, url := range options.BackupNATSServerURLs {
		if err := utils.ValidateURL(url, fmt.Sprintf("backup NATS server URL[%d]", i)); err != nil {
			return err
		}
	}
	
	if err := utils.ValidatePositiveDuration(options.PublishQueueInterval, "publish queue interval"); err != nil {
		return err
	}
	
	if err := utils.ValidatePositiveDuration(options.DebounceInterval, "debounce interval"); err != nil {
		return err
	}

	return nil
}

// GetDefaultOperatorName returns the default operator name
func GetDefaultOperatorName() string {
	return DefaultOperatorName
}

// GetDefaultCollectionNames returns the default collection names
func GetDefaultCollectionNames() (user, role, account string) {
	return DefaultUserCollectionName, DefaultRoleCollectionName, DefaultAccountCollectionName
}

// Version information
const Version = "1.0.0"

// GetVersion returns the library version
func GetVersion() string {
	return Version
}
