package pbnats

import "time"

// Options allows customizing the behavior of the NATS JWT synchronization
type Options struct {
	// Collection names
	UserCollectionName         string
	RoleCollectionName         string
	OrganizationCollectionName string
	
	// NATS configuration  
	OperatorName              string
	NATSServerURL             string
	BackupNATSServerURLs      []string
	
	// JWT settings
	DefaultJWTExpiry          time.Duration // Default: 0 (never expires)
	
	// Performance
	PublishQueueInterval time.Duration // How often to process the publish queue
	DebounceInterval     time.Duration // Wait time after changes before processing
	LogToConsole         bool
	
	// Default permissions (organization-scoped)
	DefaultOrgPublish     string   // "{org}.>"
	DefaultOrgSubscribe   []string // ["{org}.>", "_INBOX.>"]
	DefaultUserPublish    string   // "{org}.user.{user}.>"
	DefaultUserSubscribe  []string // ["{org}.>", "_INBOX.>"]
	
	// Custom event filter function
	// Return true to process the event, false to ignore
	EventFilter func(collectionName, eventType string) bool
}

// DefaultOptions returns sensible defaults for the NATS JWT synchronization options
func DefaultOptions() Options {
	return Options{
		UserCollectionName:         DefaultUserCollectionName,
		RoleCollectionName:         DefaultRoleCollectionName,
		OrganizationCollectionName: DefaultOrganizationCollectionName,
		
		OperatorName:              DefaultOperatorName,
		NATSServerURL:             "nats://localhost:4222",
		BackupNATSServerURLs:      []string{},
		
		DefaultJWTExpiry:          0, // Never expires
		PublishQueueInterval:      30 * time.Second,
		DebounceInterval:          3 * time.Second,
		LogToConsole:              true,
		
		DefaultOrgPublish:     DefaultOrgPublish,
		DefaultOrgSubscribe:   DefaultOrgSubscribe,
		DefaultUserPublish:    DefaultUserPublish,
		DefaultUserSubscribe:  DefaultUserSubscribe,
		
		EventFilter:               nil, // No filter by default, process all events
	}
}

// applyDefaultOptions fills in default values for any missing options
func applyDefaultOptions(options Options) Options {
	defaults := DefaultOptions()

	// Apply collection names
	if options.UserCollectionName == "" {
		options.UserCollectionName = defaults.UserCollectionName
	}
	if options.RoleCollectionName == "" {
		options.RoleCollectionName = defaults.RoleCollectionName
	}
	if options.OrganizationCollectionName == "" {
		options.OrganizationCollectionName = defaults.OrganizationCollectionName
	}

	// Apply NATS configuration
	if options.OperatorName == "" {
		options.OperatorName = defaults.OperatorName
	}
	if options.NATSServerURL == "" {
		options.NATSServerURL = defaults.NATSServerURL
	}

	// Apply timing intervals
	if options.PublishQueueInterval <= 0 {
		options.PublishQueueInterval = defaults.PublishQueueInterval
	}
	if options.DebounceInterval <= 0 {
		options.DebounceInterval = defaults.DebounceInterval
	}

	// Apply default permissions if not provided
	if options.DefaultOrgPublish == "" {
		options.DefaultOrgPublish = defaults.DefaultOrgPublish
	}
	if len(options.DefaultOrgSubscribe) == 0 {
		options.DefaultOrgSubscribe = defaults.DefaultOrgSubscribe
	}
	if options.DefaultUserPublish == "" {
		options.DefaultUserPublish = defaults.DefaultUserPublish
	}
	if len(options.DefaultUserSubscribe) == 0 {
		options.DefaultUserSubscribe = defaults.DefaultUserSubscribe
	}

	return options
}
