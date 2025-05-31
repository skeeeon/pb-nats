// Package pbnats provides seamless integration between PocketBase and NATS server
// by automatically generating and managing NATS JWT authentication.
package pbnats

import (
	"fmt"
	"log"

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
// This is separated from sync manager to ensure proper initialization order
func initializeSystemComponents(app *pocketbase.PocketBase, jwtGen *jwt.Generator, nkeyManager *nkey.Manager, options Options) error {
	// This logic is moved from sync/manager.go to ensure it happens at the right time
	// in the initialization sequence, before the publisher starts processing
	
	systemInit := &systemComponentInitializer{
		app:         app,
		jwtGen:      jwtGen,
		nkeyManager: nkeyManager,
		options:     options,
	}
	
	return systemInit.initialize()
}

// systemComponentInitializer handles the creation of system components
type systemComponentInitializer struct {
	app         *pocketbase.PocketBase
	jwtGen      *jwt.Generator
	nkeyManager *nkey.Manager
	options     Options
}

// initialize creates all system components in the correct order
func (sci *systemComponentInitializer) initialize() error {
	// Create system operator first
	operator, err := sci.ensureSystemOperator()
	if err != nil {
		return fmt.Errorf("failed to ensure system operator: %w", err)
	}

	// Create system account
	sysAccountID, sysAccountPubKey, err := sci.ensureSystemAccount(operator)
	if err != nil {
		return fmt.Errorf("failed to ensure system account: %w", err)
	}

	// Update operator JWT with system account reference
	if err := sci.updateOperatorJWT(operator.ID, sysAccountPubKey); err != nil {
		return fmt.Errorf("failed to update operator JWT: %w", err)
	}

	// Create system role
	sysRoleID, err := sci.ensureSystemRole()
	if err != nil {
		return fmt.Errorf("failed to ensure system role: %w", err)
	}

	// Create system user
	if err := sci.ensureSystemUser(sysAccountID, sysRoleID); err != nil {
		return fmt.Errorf("failed to ensure system user: %w", err)
	}

	return nil
}

// Placeholder methods - the actual implementation would be moved from sync/manager.go
// These would contain the system component creation logic but with better error handling
func (sci *systemComponentInitializer) ensureSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	// Implementation would be moved from sync/manager.go
	// This is just a placeholder to show the structure
	return nil, fmt.Errorf("implementation needed")
}

func (sci *systemComponentInitializer) ensureSystemAccount(operator *pbtypes.SystemOperatorRecord) (string, string, error) {
	return "", "", fmt.Errorf("implementation needed")
}

func (sci *systemComponentInitializer) updateOperatorJWT(operatorID, systemAccountPubKey string) error {
	return fmt.Errorf("implementation needed")
}

func (sci *systemComponentInitializer) ensureSystemRole() (string, error) {
	return "", fmt.Errorf("implementation needed")
}

func (sci *systemComponentInitializer) ensureSystemUser(sysAccountID, sysRoleID string) error {
	return fmt.Errorf("implementation needed")
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
