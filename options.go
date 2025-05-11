package pbnats

import "time"

// Options allows customizing the behavior of the NATS synchronization.
type Options struct {
	// Collection names to monitor
	UserCollectionName string
	RoleCollectionName string

	// File paths
	ConfigFilePath     string
	BackupDirPath      string

	// Reloading
	ReloadCommand    string
	DebounceInterval time.Duration // Time to wait before applying multiple changes

	// Default permissions
	DefaultPublish   interface{} // String or []string
	DefaultSubscribe interface{} // String or []string

	// Custom event filter function
	// Return true to process the event, false to ignore
	EventFilter func(collectionName, eventType string) bool

	// Whether to log events to console
	LogToConsole bool

	// Whether to create a backup before writing a new config
	CreateBackups bool

	// Max number of backup files to keep (0 = unlimited)
	MaxBackups int

	// Whether to initialize config on startup
	GenerateInitialConfig bool
}

// DefaultOptions returns sensible defaults for the NATS synchronization options.
func DefaultOptions() Options {
	return Options{
		UserCollectionName:    DefaultUserCollectionName,
		RoleCollectionName:    DefaultRoleCollectionName,
		ConfigFilePath:        "/etc/nats/mqtt-auth.conf",
		BackupDirPath:         "/etc/nats/backups",
		ReloadCommand:         "nats-server --signal reload",
		DebounceInterval:      3 * time.Second,
		DefaultPublish:        "PUBLIC.>",
		DefaultSubscribe:      []string{"PUBLIC.>", "_INBOX.>"},
		EventFilter:           nil, // No filter by default, process all events
		LogToConsole:          true,
		CreateBackups:         true,
		MaxBackups:            30,  // Keep last 30 backups
		GenerateInitialConfig: true, // Generate config on startup
	}
}

// applyDefaultOptions fills in default values for any missing options.
func applyDefaultOptions(options Options) Options {
	defaults := DefaultOptions()

	// Apply collection names
	if options.UserCollectionName == "" {
		options.UserCollectionName = defaults.UserCollectionName
	}
	if options.RoleCollectionName == "" {
		options.RoleCollectionName = defaults.RoleCollectionName
	}

	// Apply file paths
	if options.ConfigFilePath == "" {
		options.ConfigFilePath = defaults.ConfigFilePath
	}
	if options.BackupDirPath == "" {
		options.BackupDirPath = defaults.BackupDirPath
	}

	// Apply reload command
	if options.ReloadCommand == "" {
		options.ReloadCommand = defaults.ReloadCommand
	}

	// Apply debounce interval
	if options.DebounceInterval <= 0 {
		options.DebounceInterval = defaults.DebounceInterval
	}

	// Apply default permissions if not provided
	if options.DefaultPublish == nil {
		options.DefaultPublish = defaults.DefaultPublish
	}
	if options.DefaultSubscribe == nil {
		options.DefaultSubscribe = defaults.DefaultSubscribe
	}

	// Keep custom event filter if provided

	return options
}
