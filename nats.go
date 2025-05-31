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
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		// Wait for bootstrap to complete first
		if err := e.Next(); err != nil {
			return err
		}

		// Initialize components
		if err := initializeComponents(app, options); err != nil {
			// Log error but don't fail startup completely
			if options.LogToConsole {
				log.Printf("NATS sync initialization warning: %v", err)
			}
			return nil // Allow app to continue starting
		}

		return nil
	})

	if options.LogToConsole {
		log.Printf("PocketBase NATS JWT sync scheduled for initialization")
		log.Printf("  - User collection: %s", options.UserCollectionName)
		log.Printf("  - Role collection: %s", options.RoleCollectionName)
		log.Printf("  - Organization collection: %s", options.OrganizationCollectionName)
		log.Printf("  - NATS server: %s", options.NATSServerURL)
		log.Printf("  - Operator: %s", options.OperatorName)
	}

	return nil
}

// initializeComponents initializes all NATS sync components
func initializeComponents(app *pocketbase.PocketBase, options Options) error {
	// 1. Initialize collections first
	collectionManager := collections.NewManager(app, options)
	if err := collectionManager.InitializeCollections(); err != nil {
		return fmt.Errorf("failed to initialize collections: %w", err)
	}
	
	if options.LogToConsole {
		log.Printf("Collections initialized successfully")
	}

	// 2. Initialize NKey manager
	nkeyManager := nkey.NewManager()

	// 3. Initialize JWT generator  
	jwtGenerator := jwt.NewGenerator(app, nkeyManager, options)

	// 4. Initialize account publisher
	accountPublisher := publisher.NewManager(app, options)
	
	// Start the publisher (background queue processor)
	if err := accountPublisher.Start(); err != nil {
		return fmt.Errorf("failed to start account publisher: %w", err)
	}

	// 5. Initialize sync manager (sets up all hooks)
	syncManager := sync.NewManager(app, jwtGenerator, nkeyManager, accountPublisher, options)
	if err := syncManager.Setup(); err != nil {
		return fmt.Errorf("failed to setup sync manager: %w", err)
	}

	// 6. Setup cleanup on app termination
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// Register cleanup handler
		defer accountPublisher.Stop()
		return e.Next()
	})

	if options.LogToConsole {
		log.Printf("NATS JWT sync fully initialized and ready")
		log.Printf("  - System operator: %s", options.OperatorName)
		log.Printf("  - Queue processing interval: %v", options.PublishQueueInterval)
		log.Printf("  - Debounce interval: %v", options.DebounceInterval)
	}

	return nil
}

// validateOptions validates the provided options
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
		return fmt.Errorf("publish queue interval must be positive")
	}
	
	if options.DebounceInterval <= 0 {
		return fmt.Errorf("debounce interval must be positive")
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
