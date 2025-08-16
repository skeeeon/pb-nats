// Package types defines all shared types used throughout the pb-nats library
package types

import (
	"encoding/json"
	"strings"
	"time"
)

// AccountRecord represents a NATS account that provides isolation boundary for users.
// Accounts are the fundamental unit of tenant isolation in NATS - users within an
// account can communicate freely, while users in different accounts are isolated.
//
// ACCOUNT ISOLATION STRATEGY:
// Instead of complex subject scoping (e.g., "tenant_a.sensors.temperature"),
// accounts provide natural boundaries where subjects are simple within accounts
// (e.g., just "sensors.temperature").
//
// KEY ARCHITECTURE:
// - Main key pair: Account identity (public key is the account identifier)
// - Signing key pair: Used to sign user JWTs within this account
// - JWT: Account definition published to NATS server
//
// SIGNING KEY ROTATION:
// The rotate_keys field triggers emergency key rotation that immediately
// invalidates all user JWTs in the account (they were signed with old key).
//
// ACCOUNT LIMITS:
// Account-level limits control resources across the entire account:
// - Connection limits: Total concurrent connections to the account
// - Data limits: Total bytes in-flight across all users in account
// - Subscription limits: Total subscriptions across all users in account
// - JetStream limits: Storage and stream limits for the account
type AccountRecord struct {
	ID                string    `json:"id"`                  // Database primary key
	Name              string    `json:"name"`                // Display name (normalized for NATS account name)
	Description       string    `json:"description"`         // Human-readable description
	PublicKey         string    `json:"public_key"`          // Account public key (NATS account identifier)
	PrivateKey        string    `json:"private_key"`         // Account private key (plaintext storage)
	Seed              string    `json:"seed"`                // Account seed (plaintext storage)
	SigningPublicKey  string    `json:"signing_public_key"`  // Signing public key (for user JWT validation)
	SigningPrivateKey string    `json:"signing_private_key"` // Signing private key (plaintext storage)
	SigningSeed       string    `json:"signing_seed"`        // Signing seed (plaintext storage)
	JWT               string    `json:"jwt"`                 // Generated account JWT for NATS server
	Active            bool      `json:"active"`              // Account enable/disable flag
	RotateKeys        bool      `json:"rotate_keys"`         // Trigger field for signing key rotation
	
	// Account-level limits (optional, default to -1 = unlimited)
	MaxConnections                int64 `json:"max_connections"`                 // Max concurrent connections to account (-1 = unlimited)
	MaxSubscriptions              int64 `json:"max_subscriptions"`               // Max subscriptions across account (-1 = unlimited)
	MaxData                       int64 `json:"max_data"`                        // Max bytes in-flight across account (-1 = unlimited)
	MaxPayload                    int64 `json:"max_payload"`                     // Max message size for account (-1 = unlimited)
	MaxJetStreamDiskStorage       int64 `json:"max_jetstream_disk_storage"`      // Max JetStream disk storage (-1 = unlimited)
	MaxJetStreamMemoryStorage     int64 `json:"max_jetstream_memory_storage"`    // Max JetStream memory storage (-1 = unlimited)
	
	Created           time.Time `json:"created"`             // Creation timestamp
	Updated           time.Time `json:"updated"`             // Last update timestamp
}

// NatsUserRecord represents a NATS user with PocketBase authentication integration.
// This combines standard PocketBase auth fields with NATS-specific credentials.
//
// AUTHENTICATION DUAL MODE:
// - PocketBase API: Uses email/password for REST API access
// - NATS Connection: Uses nats_username/JWT for NATS protocol access
//
// JWT LIFECYCLE:
// User JWTs are signed by their account's signing key and contain role-based
// permissions. When account signing keys rotate, all user JWTs become invalid.
//
// CREDENTIAL REGENERATION:
// The regenerate field triggers fresh JWT generation, useful for:
// - Credential rotation after security incidents
// - Permission updates that require new JWT
// - Manual credential refresh
type NatsUserRecord struct {
	// Standard PocketBase auth fields
	ID       string `json:"id"`                             // Database primary key
	Email    string `json:"email"`                          // PocketBase authentication email
	Password string `json:"password"`                       // PocketBase password (for API auth)
	Verified bool   `json:"verified"`                       // Email verification status
	
	// NATS-specific fields (no encryption - plaintext storage by design)
	NatsUsername     string     `json:"nats_username"`      // NATS username (separate from email)
	Description      string     `json:"description"`        // Human-readable description
	PublicKey        string     `json:"public_key"`         // User public key (NATS user identifier)
	PrivateKey       string     `json:"private_key"`        // User private key (plaintext storage)
	Seed             string     `json:"seed"`               // User seed (plaintext storage)
	AccountID        string     `json:"account_id"`         // Foreign key to accounts table
	RoleID           string     `json:"role_id"`            // Foreign key to roles table
	JWT              string     `json:"jwt"`                // Generated user JWT for NATS connection
	CredsFile        string     `json:"creds_file"`         // Complete .creds file for NATS clients
	BearerToken      bool       `json:"bearer_token"`       // NATS bearer token authentication flag
	JWTExpiresAt     *time.Time `json:"jwt_expires_at"`     // Optional JWT expiration (nil = never expires)
	Regenerate       bool       `json:"regenerate"`         // Trigger field for JWT regeneration
	Active           bool       `json:"active"`             // User enable/disable flag
}

// RoleRecord represents a NATS role with permissions and per-user limits.
// Roles are permission templates that can be applied to users across different accounts.
//
// PERMISSION PHILOSOPHY:
// Permissions are stored as JSON arrays of NATS subjects. No scoping is applied
// at the role level because accounts provide natural isolation boundaries.
//
// USER LIMIT ENFORCEMENT:
// Role limits are applied per-user and control individual user resource usage:
// - max_subscriptions: Concurrent subscriptions per user
// - max_data: Total bytes user can have in-flight
// - max_payload: Maximum size of individual messages
// - -1 values indicate unlimited (maps to jwt.NoLimit)
//
// CROSS-ACCOUNT USAGE:
// The same role can be used by users in different accounts. The permissions
// are the same, but account isolation ensures no cross-tenant access.
type RoleRecord struct {
	ID                   string          `json:"id"`                     // Database primary key
	Name                 string          `json:"name"`                   // Role name
	PublishPermissions   json.RawMessage `json:"publish_permissions"`    // JSON array of publish subjects
	SubscribePermissions json.RawMessage `json:"subscribe_permissions"`  // JSON array of subscribe subjects
	Description          string          `json:"description"`            // Human-readable description
	IsDefault            bool            `json:"is_default"`             // Default role flag
	MaxSubscriptions     int64           `json:"max_subscriptions"`      // Max subscriptions per user (-1 = unlimited)
	MaxData              int64           `json:"max_data"`               // Data limit in bytes per user (-1 = unlimited)
	MaxPayload           int64           `json:"max_payload"`            // Message size limit per user (-1 = unlimited)
}

// SystemOperatorRecord represents the system operator (root of trust).
// This is the foundational cryptographic identity that signs all account JWTs.
//
// ROOT OF TRUST:
// The operator is the highest level in the NATS JWT hierarchy:
// Operator JWT → Account JWTs → User JWTs
//
// NATS SERVER INTEGRATION:
// The operator JWT is configured in nats.conf and enables the NATS server
// to validate account JWTs signed by the operator's signing keys.
//
// SINGLE OPERATOR DESIGN:
// Each pb-nats deployment has exactly one operator. Multiple operators
// would require more complex key management and are not currently supported.
type SystemOperatorRecord struct {
	ID                string    `json:"id"`                  // Database primary key
	Name              string    `json:"name"`                // Operator name (from configuration)
	PublicKey         string    `json:"public_key"`          // Operator public key
	PrivateKey        string    `json:"private_key"`         // Operator private key (plaintext storage)
	Seed              string    `json:"seed"`                // Operator seed (plaintext storage)
	SigningPublicKey  string    `json:"signing_public_key"`  // Signing public key (for account JWT validation)
	SigningPrivateKey string    `json:"signing_private_key"` // Signing private key (plaintext storage)
	SigningSeed       string    `json:"signing_seed"`        // Signing seed (plaintext storage)
	JWT               string    `json:"jwt"`                 // Generated operator JWT for NATS server config
	Created           time.Time `json:"created"`             // Creation timestamp
	Updated           time.Time `json:"updated"`             // Last update timestamp
}

// PublishQueueRecord represents a queued account publish operation for reliability.
// The queue ensures NATS operations survive server outages and network partitions.
//
// QUEUE STATE MACHINE:
// 1. Created: attempts=0, failed_at=null (ready for processing)
// 2. Processing: attempts > 0, failed_at=null (being retried)
// 3. Failed: failed_at set (permanently failed, eligible for cleanup)
// 4. Deleted: successful operations are removed from queue
//
// RETRY STRATEGY:
// - Temporary errors increment attempts and retry later
// - Bootstrap errors don't increment attempts (wait for NATS)
// - Permanent errors or max attempts trigger failed_at timestamp
// - Old failed records are cleaned up periodically
//
// DEDUPLICATION:
// Multiple operations for the same account are deduplicated by updating
// the existing queue record instead of creating duplicates.
type PublishQueueRecord struct {
	ID        string     `json:"id"`         // Database primary key
	AccountID string     `json:"account_id"` // Foreign key to accounts table
	Action    string     `json:"action"`     // Operation type: "upsert" or "delete"
	Message   string     `json:"message"`    // Error message for failed operations
	Attempts  int        `json:"attempts"`   // Retry attempt counter
	FailedAt  *time.Time `json:"failed_at"`  // Timestamp when marked as permanently failed
	Created   time.Time  `json:"created"`    // Creation timestamp
	Updated   time.Time  `json:"updated"`    // Last update timestamp
}

// RetryConfig defines the retry and failover behavior for NATS connections.
// This configuration controls how the connection manager handles network failures.
//
// EXPONENTIAL BACKOFF:
// Retry delays increase exponentially: InitialBackoff * (BackoffMultiplier ^ attempt)
// capped at MaxBackoff to prevent excessive delays.
//
// FAILOVER STRATEGY:
// - Retry primary server MaxPrimaryRetries times with exponential backoff
// - Then attempt connection to backup servers
// - If all servers fail, enter bootstrap mode (graceful degradation)
//
// FAILBACK BEHAVIOR:
// When connected to backup server, periodically check primary health
// at FailbackInterval and switch back when primary recovers.
type RetryConfig struct {
	MaxPrimaryRetries int           `json:"max_primary_retries"` // Retries on primary before failover
	InitialBackoff    time.Duration `json:"initial_backoff"`     // Initial retry delay
	MaxBackoff        time.Duration `json:"max_backoff"`         // Maximum retry delay
	BackoffMultiplier float64       `json:"backoff_multiplier"`  // Exponential backoff multiplier
	FailbackInterval  time.Duration `json:"failback_interval"`   // Primary health check interval
}

// DefaultRetryConfig returns production-ready defaults for retry configuration.
//
// DEFAULT VALUES:
// - 4 primary retries before failover (reasonable for temporary issues)
// - 1 second initial backoff (responsive but not aggressive)
// - 8 second max backoff (prevents excessive delays)
// - 2.0 multiplier (standard exponential backoff)
// - 30 second failback checks (balances responsiveness and overhead)
//
// RETURNS:
// - RetryConfig with sensible production defaults
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxPrimaryRetries: 4,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        8 * time.Second,
		BackoffMultiplier: 2.0,
		FailbackInterval:  30 * time.Second,
	}
}

// TimeoutConfig defines timeout settings for various NATS operations.
// These timeouts prevent operations from hanging indefinitely.
//
// TIMEOUT TYPES:
// - ConnectTimeout: Maximum time to establish TCP connection
// - PublishTimeout: Maximum time for publish operations (unused with async)
// - RequestTimeout: Maximum time to wait for request responses
//
// PRODUCTION CONSIDERATIONS:
// - ConnectTimeout should be short enough for responsive failover
// - RequestTimeout should accommodate network latency and NATS processing
// - PublishTimeout mainly affects synchronous publish operations
type TimeoutConfig struct {
	ConnectTimeout time.Duration `json:"connect_timeout"` // TCP connection establishment timeout
	PublishTimeout time.Duration `json:"publish_timeout"` // Publish operation timeout (async operations)
	RequestTimeout time.Duration `json:"request_timeout"` // Request-response timeout
}

// DefaultTimeoutConfig returns production-ready defaults for timeout configuration.
//
// DEFAULT VALUES:
// - 5 second connect timeout (responsive failover)
// - 10 second publish timeout (generous for large JWTs)
// - 10 second request timeout (accommodates NATS processing)
//
// RETURNS:
// - TimeoutConfig with sensible production defaults
func DefaultTimeoutConfig() *TimeoutConfig {
	return &TimeoutConfig{
		ConnectTimeout: 5 * time.Second,
		PublishTimeout: 10 * time.Second,
		RequestTimeout: 10 * time.Second,
	}
}

// Options configures the behavior of NATS JWT synchronization.
// This is the main configuration structure passed to Setup().
//
// CONFIGURATION CATEGORIES:
// - Collection Names: Customize PocketBase collection names
// - NATS Configuration: Server URLs and connection settings
// - Connection Management: Retry, timeout, and failover behavior
// - Performance: Queue processing and debouncing intervals
// - Security: Default permissions and JWT expiration
// - Operational: Logging, cleanup, and event filtering
//
// CUSTOMIZATION PHILOSOPHY:
// All aspects of pb-nats behavior can be customized through this options
// structure, with sensible defaults provided by DefaultOptions().
type Options struct {
	// Collection names (customizable for different deployments)
	UserCollectionName    string // Default: "nats_users"
	RoleCollectionName    string // Default: "nats_roles"
	AccountCollectionName string // Default: "nats_accounts"
	
	// NATS server configuration
	OperatorName              string   // NATS operator name
	NATSServerURL             string   // Primary NATS server URL
	BackupNATSServerURLs      []string // Backup server URLs for failover
	
	// Connection management (nil values use defaults)
	ConnectionRetryConfig     *RetryConfig  // Retry and failover behavior
	ConnectionTimeouts        *TimeoutConfig // Connection timeout settings
	
	// JWT configuration
	DefaultJWTExpiry          time.Duration // JWT expiration (0 = never expires)
	
	// Performance and reliability
	PublishQueueInterval time.Duration // Queue processing frequency
	DebounceInterval     time.Duration // Change debouncing delay
	LogToConsole         bool          // Enable console logging
	
	// Failed record cleanup (prevents unbounded queue growth)
	FailedRecordCleanupInterval time.Duration // Cleanup job frequency
	FailedRecordRetentionTime   time.Duration // Failed record retention period
	
	// Default permissions applied when role permissions are empty
	// Note: Account boundaries provide isolation, these are user defaults within accounts
	DefaultPublishPermissions   []string // Default publish subjects
	DefaultSubscribePermissions []string // Default subscribe subjects
	
	// Event filtering (optional custom logic)
	// Return true to process event, false to ignore
	EventFilter func(collectionName, eventType string) bool
}

// Collection names with nats_ prefix for clear identification
const (
	DefaultAccountCollectionName      = "nats_accounts"         // Account records
	DefaultUserCollectionName         = "nats_users"            // User records (auth collection)
	DefaultRoleCollectionName         = "nats_roles"            // Role/permission templates
	SystemOperatorCollectionName      = "nats_system_operator" // System operator (hidden)
	PublishQueueCollectionName        = "nats_publish_queue"   // Reliable operation queue (hidden)
)

// Default operator name used when not specified
const (
	DefaultOperatorName = "stone-age.io"
)

// Publishing actions for queue operations
const (
	PublishActionUpsert = "upsert" // Create or update account in NATS
	PublishActionDelete = "delete" // Remove account from NATS
)

// Event types for logging and filtering
// These constants enable consistent event classification across components
const (
	EventTypeAccountCreate = "account_create" // Account creation events
	EventTypeAccountUpdate = "account_update" // Account modification events  
	EventTypeAccountDelete = "account_delete" // Account deletion events
	EventTypeUserCreate    = "user_create"    // User creation events
	EventTypeUserUpdate    = "user_update"    // User modification events
	EventTypeUserDelete    = "user_delete"    // User deletion events
	EventTypeRoleCreate    = "role_create"    // Role creation events
	EventTypeRoleUpdate    = "role_update"    // Role modification events
	EventTypeRoleDelete    = "role_delete"    // Role deletion events
)

// Standard NATS inbox pattern for request-response
const (
	DefaultInboxSubscribe = "_INBOX.>" // Standard NATS inbox pattern
)

// Default permission arrays applied when role permissions are empty.
// These provide reasonable defaults within account isolation boundaries.
var DefaultPublishPermissions = []string{">"}                    // Full publish access within account
var DefaultSubscribePermissions = []string{">", "_INBOX.>"}      // Full access + inbox within account

// System constants to eliminate magic numbers and improve maintainability
const (
	DefaultNATSTimeout        = 10 * time.Second // Standard NATS operation timeout
	DefaultNATSConnectTimeout = 5 * time.Second  // Connection establishment timeout
	MaxQueueRetries           = 5                // Maximum queue operation retries
	DefaultJWTCacheSize       = 1000             // JWT cache capacity (future use)
	MaxQueueAttempts          = 10               // Maximum queue record retry attempts
	DefaultProcessingTimeout  = 30 * time.Second // Background operation timeout
)

// GetPublishPermissions extracts publish permissions from role's JSON field.
// This handles the conversion from database JSON storage to Go string slice.
//
// JSON PARSING:
// The database stores permissions as JSON arrays of strings. This method
// safely parses the JSON and returns a Go slice for permission processing.
//
// RETURNS:
// - []string containing publish subject patterns
// - error if JSON parsing fails
//
// EMPTY HANDLING:
// Empty or null JSON returns empty slice (not error) - this triggers
// default permission application in JWT generation.
func (r *RoleRecord) GetPublishPermissions() ([]string, error) {
	var permissions []string
	if len(r.PublishPermissions) == 0 {
		return permissions, nil
	}
	
	if err := json.Unmarshal(r.PublishPermissions, &permissions); err != nil {
		return nil, err
	}
	return permissions, nil
}

// GetSubscribePermissions extracts subscribe permissions from role's JSON field.
// This handles the conversion from database JSON storage to Go string slice.
//
// JSON PARSING:
// The database stores permissions as JSON arrays of strings. This method
// safely parses the JSON and returns a Go slice for permission processing.
//
// RETURNS:
// - []string containing subscribe subject patterns
// - error if JSON parsing fails
//
// EMPTY HANDLING:
// Empty or null JSON returns empty slice (not error) - this triggers
// default permission application in JWT generation.
func (r *RoleRecord) GetSubscribePermissions() ([]string, error) {
	var permissions []string
	if len(r.SubscribePermissions) == 0 {
		return permissions, nil
	}
	
	if err := json.Unmarshal(r.SubscribePermissions, &permissions); err != nil {
		return nil, err
	}
	return permissions, nil
}

// NormalizeName creates a valid NATS account name from the display name.
// NATS account names have specific character restrictions that must be enforced.
//
// NORMALIZATION RULES:
// - Convert to lowercase for consistency
// - Replace spaces and hyphens with underscores
// - Remove non-alphanumeric characters (except underscores)
// - Provide fallback for empty results
//
// ACCOUNT ISOLATION BENEFIT:
// Since accounts provide isolation boundaries, normalized names can be simple
// without complex scoping prefixes. "My Company" → "my_company" is sufficient.
//
// PARAMETERS: None (uses a.Name field)
//
// RETURNS:
// - string: Normalized account name safe for NATS
// - Never returns empty string (uses "unnamed_account" fallback)
//
// EXAMPLES:
// - "My Company" → "my_company"
// - "Test-Account" → "test_account" 
// - "Special@#$%Chars" → "specialchars"
// - "" → "unnamed_account"
func (a *AccountRecord) NormalizeName() string {
	// Convert to lowercase and replace spaces/special chars with underscores
	name := strings.ToLower(a.Name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	
	// Remove any characters that aren't alphanumeric or underscore
	var result strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			result.WriteRune(char)
		}
	}
	
	normalized := result.String()
	
	// Ensure it's not empty
	if normalized == "" {
		return "unnamed_account"
	}
	
	return normalized
}
