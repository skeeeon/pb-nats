package pbnats

import (
	"log"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// SyncManager manages the synchronization between PocketBase and NATS
type SyncManager struct {
	generator      *Generator
	fileManager    *FileManager
	reloader       *Reloader
	options        Options
	timer          *time.Timer
	timerMutex     sync.Mutex
	isGenerating   bool
	generatingMutex sync.Mutex
}

// NewSyncManager creates a new sync manager
func NewSyncManager(generator *Generator, fileManager *FileManager, reloader *Reloader, options Options) *SyncManager {
	return &SyncManager{
		generator:   generator,
		fileManager: fileManager,
		reloader:    reloader,
		options:     options,
	}
}

// Setup sets up the hooks for synchronization
func (sm *SyncManager) Setup(app *pocketbase.PocketBase) error {
	// Setup hooks for user collection
	sm.setupUserHooks(app)

	// Setup hooks for role collection
	sm.setupRoleHooks(app)

	// Generate initial configuration if needed
	if sm.options.GenerateInitialConfig {
		if sm.options.LogToConsole {
			log.Printf("Generating initial NATS configuration...")
		}
		if err := sm.triggerSync(); err != nil {
			return err
		}
	}

	return nil
}

// setupUserHooks sets up hooks for the user collection
func (sm *SyncManager) setupUserHooks(app *pocketbase.PocketBase) {
	// Handle user creation
	app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.UserCollectionName, EventTypeUserCreate) {
			if sm.options.LogToConsole {
				log.Printf("User created: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})

	// Handle user update
	app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.UserCollectionName, EventTypeUserUpdate) {
			if sm.options.LogToConsole {
				log.Printf("User updated: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})

	// Handle user deletion
	app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.UserCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.UserCollectionName, EventTypeUserDelete) {
			if sm.options.LogToConsole {
				log.Printf("User deleted: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})
}

// setupRoleHooks sets up hooks for the role collection
func (sm *SyncManager) setupRoleHooks(app *pocketbase.PocketBase) {
	// Handle role creation
	app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.RoleCollectionName, EventTypeRoleCreate) {
			if sm.options.LogToConsole {
				log.Printf("Role created: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})

	// Handle role update
	app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.RoleCollectionName, EventTypeRoleUpdate) {
			if sm.options.LogToConsole {
				log.Printf("Role updated: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})

	// Handle role deletion
	app.OnRecordAfterDeleteSuccess().BindFunc(func(e *core.RecordEvent) error {
		// Skip wrong collections
		if e.Record.Collection().Name != sm.options.RoleCollectionName {
			return e.Next()
		}
		
		if sm.shouldHandleEvent(sm.options.RoleCollectionName, EventTypeRoleDelete) {
			if sm.options.LogToConsole {
				log.Printf("Role deleted: %s", e.Record.Id)
			}
			sm.scheduleSync()
		}
		return e.Next()
	})
}

// shouldHandleEvent determines if an event should be handled
func (sm *SyncManager) shouldHandleEvent(collectionName, eventType string) bool {
	// Use custom filter if provided
	if sm.options.EventFilter != nil {
		return sm.options.EventFilter(collectionName, eventType)
	}
	
	// Default is to handle all events
	return true
}

// scheduleSync schedules a sync operation with debouncing
func (sm *SyncManager) scheduleSync() {
	sm.timerMutex.Lock()
	defer sm.timerMutex.Unlock()

	// Cancel any existing timer
	if sm.timer != nil {
		sm.timer.Stop()
	}

	// Schedule a new sync after the debounce interval
	sm.timer = time.AfterFunc(sm.options.DebounceInterval, func() {
		if err := sm.triggerSync(); err != nil {
			log.Printf("Error during sync: %v", err)
		}
	})
}

// triggerSync performs the actual sync operation
func (sm *SyncManager) triggerSync() error {
	sm.generatingMutex.Lock()
	if sm.isGenerating {
		sm.generatingMutex.Unlock()
		return nil // Another sync is already in progress
	}
	sm.isGenerating = true
	sm.generatingMutex.Unlock()

	// Make sure to reset the flag when done
	defer func() {
		sm.generatingMutex.Lock()
		sm.isGenerating = false
		sm.generatingMutex.Unlock()
	}()

	// Generate configuration - handle failures gracefully
	config, err := sm.generator.GenerateConfig()
	if err != nil {
		if sm.options.LogToConsole {
			log.Printf("Warning: Failed to generate config: %v", err)
		}
		return err
	}

	// Check if config has changed
	changed, err := sm.fileManager.HasConfigChanged(config)
	if err != nil {
		if sm.options.LogToConsole {
			log.Printf("Warning: Failed to check if config changed: %v", err)
		}
		return err
	}

	// If config has changed, update the file and reload NATS
	if changed {
		// Write config file
		if err := sm.fileManager.WriteConfigFile(config); err != nil {
			if sm.options.LogToConsole {
				log.Printf("Warning: Failed to write config file: %v", err)
			}
			return err
		}

		// Reload NATS
		if err := sm.reloader.ReloadConfig(); err != nil {
			if sm.options.LogToConsole {
				log.Printf("Warning: Failed to reload NATS: %v", err)
			}
			return err
		}
	}

	return nil
}
