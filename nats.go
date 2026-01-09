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
type RetryConfig = pbtypes.RetryConfig
type TimeoutConfig = pbtypes.TimeoutConfig

// Re-export constants for external use  
const (
	DefaultAccountCollectionName = pbtypes.DefaultAccountCollectionName
	DefaultUserCollectionName    = pbtypes.DefaultUserCollectionName
	DefaultRoleCollectionName    = pbtypes.DefaultRoleCollectionName
	SystemOperatorCollectionName = pbtypes.SystemOperatorCollectionName
	PublishQueueCollectionName   = pbtypes.PublishQueueCollectionName
	
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

// Setup initializes NATS JWT synchronization for a PocketBase instance.
func Setup(app *pocketbase.PocketBase, options Options) error {
	options = applyDefaultOptions(options)
	logger := utils.NewLogger(options.LogToConsole)

	if err := validateOptions(options); err != nil {
		return utils.WrapError(err, "invalid options")
	}

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return utils.WrapError(err, "bootstrap failed")
		}

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
	logger.Info("   Primary NATS server: %s", options.NATSServerURL)
	if len(options.BackupNATSServerURLs) > 0 {
		logger.Info("   Backup NATS servers: %v", options.BackupNATSServerURLs)
	}
	logger.Info("   Operator: %s", options.OperatorName)

	return nil
}

// initializeComponents initializes all NATS sync components in dependency order.
func initializeComponents(app *pocketbase.PocketBase, options Options, logger *utils.Logger) error {
	logger.Process("Initializing NATS sync components...")

	// Step 1: Initialize collections
	logger.Info("   Creating collections...")
	collectionManager := collections.NewManager(app, options)
	if err := collectionManager.InitializeCollections(); err != nil {
		return utils.WrapError(err, "failed to initialize collections")
	}
	logger.Success("   Collections initialized")

	// Step 2: Initialize NKey manager
	nkeyManager := nkey.NewManager()
	logger.Success("   NKey manager initialized")

	// Step 3: Initialize JWT generator
	jwtGenerator := jwt.NewGenerator(app, nkeyManager, options)
	logger.Success("   JWT generator initialized")

	// Step 4: Initialize system components
	logger.Info("   Creating system components...")
	if err := initializeSystemComponents(app, jwtGenerator, nkeyManager, options, logger); err != nil {
		return utils.WrapError(err, "failed to initialize system components")
	}
	logger.Success("   System components initialized")

	// Step 5: Initialize publisher
	logger.Info("   Starting account publisher with connection manager...")
	accountPublisher := publisher.NewManager(app, options)
	if err := accountPublisher.Start(); err != nil {
		return utils.WrapError(err, "failed to start account publisher")
	}
	logger.Success("   Account publisher started with persistent connections")

	// Step 6: Initialize sync manager
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

	return nil
}

// initializeSystemComponents creates system operator, account, role, and user if they don't exist.
func initializeSystemComponents(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, options Options, logger *utils.Logger) error {
	operatorRecords, err := app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to find system operator records")
	}

	var operator *pbtypes.SystemOperatorRecord
	var sysAccountID string
	var sysAccountPubKey string

	if len(operatorRecords) == 0 {
		// Create operator without JWT initially
		operator, err = createSystemOperatorWithoutJWT(app, nkeyManager, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system operator")
		}

		// Create system account
		sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system account")
		}

		// Update operator JWT with system account reference
		if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey, logger); err != nil {
			return utils.WrapError(err, "failed to update operator JWT")
		}

		logger.Success("Created and configured system operator with system account reference")
	} else {
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

		sysAccountRecords, err := app.FindAllRecords(options.AccountCollectionName, dbx.HashExp{"name": "System Account"})
		if err != nil {
			return utils.WrapError(err, "failed to find system account records")
		}

		if len(sysAccountRecords) == 0 {
			sysAccountID, sysAccountPubKey, err = createSystemAccount(app, jwtGen, nkeyManager, operator, options, logger)
			if err != nil {
				return utils.WrapError(err, "failed to create system account")
			}

			if err := updateOperatorJWT(app, jwtGen, operator.ID, sysAccountPubKey, logger); err != nil {
				return utils.WrapError(err, "failed to update operator JWT")
			}
		} else {
			sysAccountID = sysAccountRecords[0].Id
			sysAccountPubKey = sysAccountRecords[0].GetString("public_key")
		}
	}

	// Check if system role exists
	sysRoleRecords, err := app.FindAllRecords(options.RoleCollectionName, dbx.HashExp{"name": "system_admin"})
	if err != nil {
		return utils.WrapError(err, "failed to find system role records")
	}

	var sysRoleID string
	if len(sysRoleRecords) == 0 {
		sysRoleID, err = createSystemRole(app, options, logger)
		if err != nil {
			return utils.WrapError(err, "failed to create system role")
		}
	} else {
		sysRoleID = sysRoleRecords[0].Id
	}

	// Check if system user exists
	sysUserRecords, err := app.FindAllRecords(options.UserCollectionName, dbx.HashExp{"nats_username": "sys", "account_id": sysAccountID})
	if err != nil {
		return utils.WrapError(err, "failed to find system user records")
	}

	if len(sysUserRecords) == 0 {
		if err := createSystemUser(app, jwtGen, nkeyManager, sysAccountID, sysRoleID, options, logger); err != nil {
			return utils.WrapError(err, "failed to create system user")
		}
	}

	return nil
}

// createSystemOperatorWithoutJWT creates the system operator record with keys but no JWT.
func createSystemOperatorWithoutJWT(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options Options, logger *utils.Logger) (*pbtypes.SystemOperatorRecord, error) {
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateOperatorKeyPair()
	if err != nil {
		return nil, utils.WrapError(err, "failed to generate operator keys")
	}

	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return nil, utils.WrapError(err, "failed to get private key")
	}

	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return nil, utils.WrapError(err, "failed to get signing private key")
	}

	operator := &pbtypes.SystemOperatorRecord{
		Name:              options.OperatorName,
		PublicKey:         public,
		PrivateKey:        privateKey,
		Seed:              seed,
		SigningPublicKey:  signingPublic,
		SigningPrivateKey: signingPrivateKey,
		SigningSeed:       signingKey,
		JWT:               "",
	}

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
	record.Set("jwt", "")

	if err := app.Save(record); err != nil {
		return nil, utils.WrapError(err, "failed to save system operator")
	}

	operator.ID = record.Id
	logger.Success("Created system operator (without JWT): %s", operator.Name)

	return operator, nil
}

// updateOperatorJWT generates and saves the final operator JWT with system account reference.
func updateOperatorJWT(app *pocketbase.PocketBase, jwtGen *jwt.Generator, operatorID, systemAccountPubKey string, logger *utils.Logger) error {
	operatorRecord, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, operatorID)
	if err != nil {
		return utils.WrapError(err, "failed to find operator record")
	}

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

	jwtValue, err := jwtGen.GenerateOperatorJWT(operator, systemAccountPubKey)
	if err != nil {
		return utils.WrapError(err, "failed to generate operator JWT")
	}

	operatorRecord.Set("jwt", jwtValue)
	if err := app.Save(operatorRecord); err != nil {
		return utils.WrapError(err, "failed to save updated operator JWT")
	}

	return nil
}

// createSystemAccount creates the system account (SYS) for NATS management operations.
func createSystemAccount(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, operator *pbtypes.SystemOperatorRecord, options Options, logger *utils.Logger) (string, string, error) {
	seed, public, signingKey, signingPublic, err := nkeyManager.GenerateAccountKeyPair()
	if err != nil {
		return "", "", utils.WrapError(err, "failed to generate account keys")
	}

	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to get private key")
	}

	signingPrivateKey, err := nkeyManager.GetPrivateKeyFromSeed(signingKey)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to get signing private key")
	}

	sysAccount := &pbtypes.AccountRecord{
		Name:                      "System Account",
		Description:               "Automatically created system account for NATS management",
		PublicKey:                 public,
		PrivateKey:                privateKey,
		Seed:                      seed,
		SigningPublicKey:          signingPublic,
		SigningPrivateKey:         signingPrivateKey,
		SigningSeed:               signingKey,
		Active:                    true,
		MaxConnections:            -1,
		MaxSubscriptions:          -1,
		MaxData:                   -1,
		MaxPayload:                -1,
		MaxJetStreamDiskStorage:   -1,
		MaxJetStreamMemoryStorage: -1,
	}

	jwtValue, err := jwtGen.GenerateSystemAccountJWT(sysAccount, operator.SigningSeed)
	if err != nil {
		return "", "", utils.WrapError(err, "failed to generate system account JWT")
	}
	sysAccount.JWT = jwtValue

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
	record.Set("max_connections", sysAccount.MaxConnections)
	record.Set("max_subscriptions", sysAccount.MaxSubscriptions)
	record.Set("max_data", sysAccount.MaxData)
	record.Set("max_payload", sysAccount.MaxPayload)
	record.Set("max_jetstream_disk_storage", sysAccount.MaxJetStreamDiskStorage)
	record.Set("max_jetstream_memory_storage", sysAccount.MaxJetStreamMemoryStorage)

	if err := app.Save(record); err != nil {
		return "", "", utils.WrapError(err, "failed to save system account")
	}

	logger.Success("Created system account: %s (Public Key: %s)", sysAccount.NormalizeName(), sysAccount.PublicKey)

	return record.Id, sysAccount.PublicKey, nil
}

// createSystemRole creates the system administrator role with full NATS access.
// System role has response permissions enabled by default.
func createSystemRole(app *pocketbase.PocketBase, options Options, logger *utils.Logger) (string, error) {
	collection, err := app.FindCollectionByNameOrId(options.RoleCollectionName)
	if err != nil {
		return "", utils.WrapError(err, "failed to find roles collection")
	}

	record := core.NewRecord(collection)
	record.Set("name", "system_admin")
	record.Set("description", "System administrator role with full NATS access")
	record.Set("publish_permissions", `["$SYS.>", ">"]`)
	record.Set("subscribe_permissions", `["$SYS.>", ">"]`)
	record.Set("publish_deny_permissions", "[]")
	record.Set("subscribe_deny_permissions", "[]")
	record.Set("is_default", false)
	
	// System role has response permissions enabled by default
	record.Set("allow_response", true)
	record.Set("allow_response_max", -1) // Unlimited responses
	record.Set("allow_response_ttl", 0)  // No TTL limit
	
	// Unlimited limits for system operations
	record.Set("max_subscriptions", -1)
	record.Set("max_data", -1)
	record.Set("max_payload", -1)

	if err := app.Save(record); err != nil {
		return "", utils.WrapError(err, "failed to save system role")
	}

	logger.Success("Created system role: system_admin")

	return record.Id, nil
}

// createSystemUser creates the system user for internal NATS operations.
func createSystemUser(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, sysAccountID, sysRoleID string, options Options, logger *utils.Logger) error {
	collection, err := app.FindCollectionByNameOrId(options.UserCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to find users collection")
	}

	record := core.NewRecord(collection)
	record.Set("email", "system@localhost.com")
	record.Set("password", "system-generated-password-"+time.Now().Format("20060102150405"))
	record.Set("verified", true)
	record.Set("nats_username", "sys")
	record.Set("description", "System user for NATS management operations")
	record.Set("account_id", sysAccountID)
	record.Set("role_id", sysRoleID)
	record.Set("bearer_token", false)
	record.Set("regenerate", false)
	record.Set("active", true)

	if err := generateUserKeys(app, jwtGen, nkeyManager, record, options); err != nil {
		return utils.WrapError(err, "failed to generate system user keys")
	}

	if err := app.Save(record); err != nil {
		return utils.WrapError(err, "failed to save system user")
	}

	logger.Success("Created system user: sys")

	return nil
}

// generateUserKeys generates NATS keys, JWT, and .creds file for a user record.
func generateUserKeys(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, record *core.Record, options Options) error {
	if record.GetString("public_key") != "" {
		return nil
	}

	seed, public, err := nkeyManager.GenerateUserKeyPair()
	if err != nil {
		return utils.WrapError(err, "failed to generate user key pair")
	}

	privateKey, err := nkeyManager.GetPrivateKeyFromSeed(seed)
	if err != nil {
		return utils.WrapError(err, "failed to get private key from seed")
	}

	record.Set("public_key", public)
	record.Set("private_key", privateKey)
	record.Set("seed", seed)

	return generateUserJWT(app, jwtGen, record, options)
}

// generateUserJWT generates JWT and .creds file for a user based on their account and role.
func generateUserJWT(app *pocketbase.PocketBase, jwtGen *jwt.Generator, record *core.Record, options Options) error {
	account, err := app.FindRecordById(options.AccountCollectionName, record.GetString("account_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find account %s", record.GetString("account_id"))
	}

	role, err := app.FindRecordById(options.RoleCollectionName, record.GetString("role_id"))
	if err != nil {
		return utils.WrapErrorf(err, "failed to find role %s", record.GetString("role_id"))
	}

	user := recordToUserModel(record)
	accountModel := recordToAccountModel(account)
	roleModel := recordToRoleModel(role)

	jwtValue, err := jwtGen.GenerateUserJWT(user, accountModel, roleModel)
	if err != nil {
		return utils.WrapError(err, "failed to generate user JWT")
	}

	record.Set("jwt", jwtValue)
	user.JWT = jwtValue

	credsFile, err := jwtGen.GenerateCredsFile(user)
	if err != nil {
		return utils.WrapError(err, "failed to generate creds file")
	}

	record.Set("creds_file", credsFile)
	return nil
}

// recordToUserModel converts PocketBase user record to internal user model.
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

// recordToAccountModel converts PocketBase account record to internal account model.
func recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
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
// Updated to include deny permissions and response permission fields.
func recordToRoleModel(record *core.Record) *pbtypes.RoleRecord {
	return &pbtypes.RoleRecord{
		ID:                       record.Id,
		Name:                     record.GetString("name"),
		PublishPermissions:       []byte(record.GetString("publish_permissions")),
		SubscribePermissions:     []byte(record.GetString("subscribe_permissions")),
		PublishDenyPermissions:   []byte(record.GetString("publish_deny_permissions")),
		SubscribeDenyPermissions: []byte(record.GetString("subscribe_deny_permissions")),
		AllowResponse:            record.GetBool("allow_response"),
		AllowResponseMax:         record.GetInt("allow_response_max"),
		AllowResponseTTL:         record.GetInt("allow_response_ttl"),
		MaxSubscriptions:         int64(record.GetInt("max_subscriptions")),
		MaxData:                  int64(record.GetInt("max_data")),
		MaxPayload:               int64(record.GetInt("max_payload")),
	}
}

// validateOptions validates the provided options.
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
	if err := utils.ValidatePositiveDuration(options.FailedRecordCleanupInterval, "failed record cleanup interval"); err != nil {
		return err
	}
	if err := utils.ValidatePositiveDuration(options.FailedRecordRetentionTime, "failed record retention time"); err != nil {
		return err
	}

	if options.ConnectionRetryConfig != nil {
		if options.ConnectionRetryConfig.MaxPrimaryRetries < 0 {
			return fmt.Errorf("max primary retries must be non-negative, got: %d", options.ConnectionRetryConfig.MaxPrimaryRetries)
		}
		if err := utils.ValidatePositiveDuration(options.ConnectionRetryConfig.InitialBackoff, "initial backoff"); err != nil {
			return err
		}
		if err := utils.ValidatePositiveDuration(options.ConnectionRetryConfig.MaxBackoff, "max backoff"); err != nil {
			return err
		}
		if options.ConnectionRetryConfig.BackoffMultiplier <= 0 {
			return fmt.Errorf("backoff multiplier must be positive, got: %f", options.ConnectionRetryConfig.BackoffMultiplier)
		}
		if err := utils.ValidatePositiveDuration(options.ConnectionRetryConfig.FailbackInterval, "failback interval"); err != nil {
			return err
		}
	}
	
	if options.ConnectionTimeouts != nil {
		if err := utils.ValidatePositiveDuration(options.ConnectionTimeouts.ConnectTimeout, "connect timeout"); err != nil {
			return err
		}
		if err := utils.ValidatePositiveDuration(options.ConnectionTimeouts.PublishTimeout, "publish timeout"); err != nil {
			return err
		}
		if err := utils.ValidatePositiveDuration(options.ConnectionTimeouts.RequestTimeout, "request timeout"); err != nil {
			return err
		}
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
const Version = "1.1.0"

// GetVersion returns the library version
func GetVersion() string {
	return Version
}
