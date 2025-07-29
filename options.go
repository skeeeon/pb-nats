package pbnats

import (
	"time"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// DefaultOptions returns sensible defaults for the NATS JWT synchronization options
func DefaultOptions() Options {
	return Options{
		UserCollectionName:    pbtypes.DefaultUserCollectionName,
		RoleCollectionName:    pbtypes.DefaultRoleCollectionName,
		AccountCollectionName: pbtypes.DefaultAccountCollectionName,
		
		OperatorName:              pbtypes.DefaultOperatorName,
		NATSServerURL:             "nats://localhost:4222",
		BackupNATSServerURLs:      []string{},
		
		DefaultJWTExpiry:          0, // Never expires
		PublishQueueInterval:      30 * time.Second,
		DebounceInterval:          3 * time.Second,
		LogToConsole:              true,
		
		// Default permissions without scoping - accounts provide isolation
		DefaultAccountPublish:     pbtypes.DefaultAccountPublish,
		DefaultAccountSubscribe:   pbtypes.DefaultAccountSubscribe,
		DefaultUserPublish:        pbtypes.DefaultUserPublish,
		DefaultUserSubscribe:      pbtypes.DefaultUserSubscribe,
		
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
	if options.AccountCollectionName == "" {
		options.AccountCollectionName = defaults.AccountCollectionName
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
	if options.DefaultAccountPublish == "" {
		options.DefaultAccountPublish = defaults.DefaultAccountPublish
	}
	if len(options.DefaultAccountSubscribe) == 0 {
		options.DefaultAccountSubscribe = defaults.DefaultAccountSubscribe
	}
	if options.DefaultUserPublish == "" {
		options.DefaultUserPublish = defaults.DefaultUserPublish
	}
	if len(options.DefaultUserSubscribe) == 0 {
		options.DefaultUserSubscribe = defaults.DefaultUserSubscribe
	}

	return options
}
