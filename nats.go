// Package pbnats provides seamless integration between PocketBase and NATS server
// by automatically generating and updating NATS MQTT authentication configuration.
package pbnats

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase"
)

// Setup initializes the NATS synchronization for a PocketBase instance.
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

	// Ensure backup directory exists if backups are enabled
	if options.CreateBackups {
		if err := os.MkdirAll(options.BackupDirPath, 0755); err != nil {
			return fmt.Errorf("failed to create backup directory: %w", err)
		}
	}

	// Ensure the directory for the config file exists
	configDir := filepath.Dir(options.ConfigFilePath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create file manager
	fileManager := NewFileManager(
		options.ConfigFilePath,
		options.BackupDirPath,
		options.MaxBackups,
		options.CreateBackups,
		options.LogToConsole,
	)

	// Create config generator
	generator := NewGenerator(
		app,
		options.UserCollectionName,
		options.RoleCollectionName,
		options.DefaultPublish,
		options.DefaultSubscribe,
		options.LogToConsole,
	)

	// Create NATS reloader
	reloader := NewReloader(
		options.ReloadCommand,
		options.DebounceInterval / 2, // Set minimum reload interval to half the debounce time
		options.LogToConsole,
	)

	// Create sync manager
	syncManager := NewSyncManager(generator, fileManager, reloader, options)

	// Register hooks and set up event handling after app is bootstrapped
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		if err := syncManager.Setup(app); err != nil {
			return fmt.Errorf("failed to setup sync manager: %w", err)
		}
		return nil
	})
	
	// No need to check for an error return from the outer syncManager.Setup call
	// since we're now registering it to run later via the hook
		return fmt.Errorf("failed to setup sync manager: %w", err)
	}

	if options.LogToConsole {
		log.Printf("PocketBase NATS sync initialized successfully")
		log.Printf("  - User collection: %s", options.UserCollectionName)
		log.Printf("  - Role collection: %s", options.RoleCollectionName)
		log.Printf("  - Config file: %s", options.ConfigFilePath)
		log.Printf("  - Debounce interval: %v", options.DebounceInterval)
	}

	return nil
}
