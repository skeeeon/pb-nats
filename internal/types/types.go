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
// ACCOUNT LIMITS (FIXED - CORRECT NATS SEMANTICS):
// Account-level limits control resources across the entire account:
// - Connection limits: Total concurrent connections to the account
// - Data limits: Total bytes in-flight across all users in account
// - Subscription limits: Total subscriptions across all users in account
// - JetStream limits: Storage and stream limits for the account
//
// LIMIT VALUES (CRITICAL - CORRECT NATS SEMANTICS):
// - (-1) = Unlimited (no restrictions) 
// - (0) = Disabled/blocked (no access allowed)
// - (positive) = Specific limit value
type AccountRecord struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	PublicKey         string    `json:"public_key"`
	PrivateKey        string    `json:"private_key"`
	Seed              string    `json:"seed"`
	SigningPublicKey  string    `json:"signing_public_key"`
	SigningPrivateKey string    `json:"signing_private_key"`
	SigningSeed       string    `json:"signing_seed"`
	JWT               string    `json:"jwt"`
	Active            bool      `json:"active"`
	RotateKeys        bool      `json:"rotate_keys"`
	
	// Account-level limits (NATS semantics: -1 = unlimited, 0 = disabled, positive = limit)
	MaxConnections            int64 `json:"max_connections"`
	MaxSubscriptions          int64 `json:"max_subscriptions"`
	MaxData                   int64 `json:"max_data"`
	MaxPayload                int64 `json:"max_payload"`
	MaxJetStreamDiskStorage   int64 `json:"max_jetstream_disk_storage"`
	MaxJetStreamMemoryStorage int64 `json:"max_jetstream_memory_storage"`
	
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
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
	ID       string `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Verified bool   `json:"verified"`
	
	// NATS-specific fields (no encryption - plaintext storage by design)
	NatsUsername string     `json:"nats_username"`
	Description  string     `json:"description"`
	PublicKey    string     `json:"public_key"`
	PrivateKey   string     `json:"private_key"`
	Seed         string     `json:"seed"`
	AccountID    string     `json:"account_id"`
	RoleID       string     `json:"role_id"`
	JWT          string     `json:"jwt"`
	CredsFile    string     `json:"creds_file"`
	BearerToken  bool       `json:"bearer_token"`
	JWTExpiresAt *time.Time `json:"jwt_expires_at"`
	Regenerate   bool       `json:"regenerate"`
	Active       bool       `json:"active"`
}

// RoleRecord represents a NATS role with permissions and per-user limits.
// Roles are permission templates that can be applied to users across different accounts.
//
// PERMISSION PHILOSOPHY:
// Permissions are stored as JSON arrays of NATS subjects. No scoping is applied
// at the role level because accounts provide natural isolation boundaries.
//
// PERMISSION TYPES:
// - Allow permissions: Subjects the user CAN access
// - Deny permissions: Subjects the user CANNOT access (evaluated after allow)
// - Response permission: Controls request-reply response behavior
//
// PERMISSION EVALUATION ORDER (NATS semantics):
// 1. Check if subject matches any Allow pattern
// 2. If allowed, check if subject matches any Deny pattern
// 3. Deny takes precedence over Allow for matching subjects
//
// USER LIMIT ENFORCEMENT (CORRECT NATS SEMANTICS):
// Role limits are applied per-user and control individual user resource usage:
// - max_subscriptions: Concurrent subscriptions per user
// - max_data: Total bytes user can have in-flight
// - max_payload: Maximum size of individual messages
//
// LIMIT VALUES (CRITICAL - CORRECT NATS SEMANTICS):
// - (-1) = Unlimited (no restrictions)
// - (0) = Disabled/blocked (no access allowed) 
// - (positive) = Specific limit value
//
// CROSS-ACCOUNT USAGE:
// The same role can be used by users in different accounts. The permissions
// are the same, but account isolation ensures no cross-tenant access.
type RoleRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	
	// Allow permissions (subjects user CAN access)
	PublishPermissions   json.RawMessage `json:"publish_permissions"`
	SubscribePermissions json.RawMessage `json:"subscribe_permissions"`
	
	// Deny permissions (subjects user CANNOT access - takes precedence over allow)
	PublishDenyPermissions   json.RawMessage `json:"publish_deny_permissions"`
	SubscribeDenyPermissions json.RawMessage `json:"subscribe_deny_permissions"`
	
	// Response permission for request-reply patterns
	// When true, allows the user to send responses to requests
	AllowResponse bool `json:"allow_response"`
	// Maximum number of responses allowed per request (-1 = unlimited, 0 = default/1)
	AllowResponseMax int `json:"allow_response_max"`
	// Response TTL in seconds (0 = no limit/default)
	AllowResponseTTL int `json:"allow_response_ttl"`
	
	// Per-user limits (NATS semantics: -1 = unlimited, 0 = disabled, positive = limit)
	MaxSubscriptions int64 `json:"max_subscriptions"`
	MaxData          int64 `json:"max_data"`
	MaxPayload       int64 `json:"max_payload"`

	// Timestamps
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
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
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	PublicKey         string    `json:"public_key"`
	PrivateKey        string    `json:"private_key"`
	Seed              string    `json:"seed"`
	SigningPublicKey  string    `json:"signing_public_key"`
	SigningPrivateKey string    `json:"signing_private_key"`
	SigningSeed       string    `json:"signing_seed"`
	JWT               string    `json:"jwt"`
	Created           time.Time `json:"created"`
	Updated           time.Time `json:"updated"`
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
	ID        string     `json:"id"`
	AccountID string     `json:"account_id"`
	Action    string     `json:"action"`
	Message   string     `json:"message"`
	Attempts  int        `json:"attempts"`
	FailedAt  *time.Time `json:"failed_at"`
	Created   time.Time  `json:"created"`
	Updated   time.Time  `json:"updated"`
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
	MaxPrimaryRetries int           `json:"max_primary_retries"`
	InitialBackoff    time.Duration `json:"initial_backoff"`
	MaxBackoff        time.Duration `json:"max_backoff"`
	BackoffMultiplier float64       `json:"backoff_multiplier"`
	FailbackInterval  time.Duration `json:"failback_interval"`
}

// DefaultRetryConfig returns production-ready defaults for retry configuration.
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
type TimeoutConfig struct {
	ConnectTimeout time.Duration `json:"connect_timeout"`
	PublishTimeout time.Duration `json:"publish_timeout"`
	RequestTimeout time.Duration `json:"request_timeout"`
}

// DefaultTimeoutConfig returns production-ready defaults for timeout configuration.
func DefaultTimeoutConfig() *TimeoutConfig {
	return &TimeoutConfig{
		ConnectTimeout: 5 * time.Second,
		PublishTimeout: 10 * time.Second,
		RequestTimeout: 10 * time.Second,
	}
}

// Options configures the behavior of NATS JWT synchronization.
type Options struct {
	// Collection names (customizable for different deployments)
	UserCollectionName    string
	RoleCollectionName    string
	AccountCollectionName string
	
	// NATS server configuration
	OperatorName         string
	NATSServerURL        string
	BackupNATSServerURLs []string
	
	// Connection management (nil values use defaults)
	ConnectionRetryConfig *RetryConfig
	ConnectionTimeouts    *TimeoutConfig
	
	// JWT configuration
	DefaultJWTExpiry time.Duration
	
	// Performance and reliability
	PublishQueueInterval time.Duration
	DebounceInterval     time.Duration
	LogToConsole         bool
	
	// Failed record cleanup
	FailedRecordCleanupInterval time.Duration
	FailedRecordRetentionTime   time.Duration
	
	// Default permissions applied when role permissions are empty
	DefaultPublishPermissions   []string
	DefaultSubscribePermissions []string
	
	// Event filtering (optional custom logic)
	EventFilter func(collectionName, eventType string) bool
}

// Collection names with nats_ prefix for clear identification
const (
	DefaultAccountCollectionName = "nats_accounts"
	DefaultUserCollectionName    = "nats_users"
	DefaultRoleCollectionName    = "nats_roles"
	SystemOperatorCollectionName = "nats_system_operator"
	PublishQueueCollectionName   = "nats_publish_queue"
)

// Default operator name used when not specified
const (
	DefaultOperatorName = "stone-age.io"
)

// Publishing actions for queue operations
const (
	PublishActionUpsert = "upsert"
	PublishActionDelete = "delete"
)

// Event types for logging and filtering
const (
	EventTypeAccountCreate = "account_create"
	EventTypeAccountUpdate = "account_update"
	EventTypeAccountDelete = "account_delete"
	EventTypeUserCreate    = "user_create"
	EventTypeUserUpdate    = "user_update"
	EventTypeUserDelete    = "user_delete"
	EventTypeRoleCreate    = "role_create"
	EventTypeRoleUpdate    = "role_update"
	EventTypeRoleDelete    = "role_delete"
)

// Standard NATS inbox pattern for request-response
const (
	DefaultInboxSubscribe = "_INBOX.>"
)

// Default permission arrays applied when role permissions are empty.
var DefaultPublishPermissions = []string{">"}
var DefaultSubscribePermissions = []string{">", "_INBOX.>"}

// System constants
const (
	DefaultNATSTimeout        = 10 * time.Second
	DefaultNATSConnectTimeout = 5 * time.Second
	MaxQueueRetries           = 5
	DefaultJWTCacheSize       = 1000
	MaxQueueAttempts          = 10
	DefaultProcessingTimeout  = 30 * time.Second
)

// GetPublishPermissions extracts publish allow permissions from role's JSON field.
func (r *RoleRecord) GetPublishPermissions() ([]string, error) {
	return parseJSONPermissions(r.PublishPermissions)
}

// GetSubscribePermissions extracts subscribe allow permissions from role's JSON field.
func (r *RoleRecord) GetSubscribePermissions() ([]string, error) {
	return parseJSONPermissions(r.SubscribePermissions)
}

// GetPublishDenyPermissions extracts publish deny permissions from role's JSON field.
func (r *RoleRecord) GetPublishDenyPermissions() ([]string, error) {
	return parseJSONPermissions(r.PublishDenyPermissions)
}

// GetSubscribeDenyPermissions extracts subscribe deny permissions from role's JSON field.
func (r *RoleRecord) GetSubscribeDenyPermissions() ([]string, error) {
	return parseJSONPermissions(r.SubscribeDenyPermissions)
}

// parseJSONPermissions is a helper function to parse JSON permission arrays.
// Handles empty/nil input gracefully.
func parseJSONPermissions(data json.RawMessage) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}
	
	var permissions []string
	if err := json.Unmarshal(data, &permissions); err != nil {
		return nil, err
	}
	return permissions, nil
}

// NormalizeName creates a valid NATS account name from the display name.
func (a *AccountRecord) NormalizeName() string {
	name := strings.ToLower(a.Name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	
	var result strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			result.WriteRune(char)
		}
	}
	
	normalized := result.String()
	if normalized == "" {
		return "unnamed_account"
	}
	return normalized
}
