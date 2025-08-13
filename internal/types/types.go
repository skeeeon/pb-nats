// Package types defines all shared types used throughout the pb-nats library
package types

import (
	"encoding/json"
	"strings"
	"time"
)

// AccountRecord represents a NATS account that provides isolation boundary
type AccountRecord struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`                // Display name (also used as NATS account name when normalized)
	Description       string    `json:"description"`
	PublicKey         string    `json:"public_key"`          // Account public key
	PrivateKey        string    `json:"private_key"`         // Account private key (plaintext)
	Seed              string    `json:"seed"`                // Account seed (plaintext)
	SigningPublicKey  string    `json:"signing_public_key"`  // Account signing public key
	SigningPrivateKey string    `json:"signing_private_key"` // Account signing private key (plaintext)
	SigningSeed       string    `json:"signing_seed"`        // Account signing seed (plaintext)
	JWT               string    `json:"jwt"`                 // Generated account JWT
	Active            bool      `json:"active"`
	Created           time.Time `json:"created"`
	Updated           time.Time `json:"updated"`
}

// NatsUserRecord represents a NATS user with PocketBase authentication
type NatsUserRecord struct {
	// Standard PocketBase auth fields
	ID       string `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"` // For PocketBase API auth
	Verified bool   `json:"verified"`
	
	// NATS-specific fields (no encryption)
	NatsUsername     string     `json:"nats_username"`     // NATS username
	Description      string     `json:"description"`
	PublicKey        string     `json:"public_key"`        // User public key
	PrivateKey       string     `json:"private_key"`       // User private key (plaintext)
	Seed             string     `json:"seed"`              // User seed (plaintext)
	AccountID        string     `json:"account_id"`        // FK to accounts
	RoleID           string     `json:"role_id"`           // FK to nats_roles
	JWT              string     `json:"jwt"`               // Generated user JWT
	CredsFile        string     `json:"creds_file"`        // Complete .creds file
	BearerToken      bool       `json:"bearer_token"`      // Bearer token flag
	JWTExpiresAt     *time.Time `json:"jwt_expires_at"`    // Optional expiration
	Regenerate       bool       `json:"regenerate"`        // Triggers JWT regeneration when set to true
	Active           bool       `json:"active"`
}

// RoleRecord represents a NATS role with permissions
type RoleRecord struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	PublishPermissions   json.RawMessage `json:"publish_permissions"`   // JSON array of strings
	SubscribePermissions json.RawMessage `json:"subscribe_permissions"` // JSON array of strings
	Description          string          `json:"description"`
	IsDefault            bool            `json:"is_default"`
	MaxConnections       int64           `json:"max_connections"`       // -1 for unlimited
	MaxData              int64           `json:"max_data"`              // -1 for unlimited
	MaxPayload           int64           `json:"max_payload"`           // -1 for unlimited
}

// SystemOperatorRecord represents the system operator (single record)
type SystemOperatorRecord struct {
	ID                string `json:"id"`
	Name              string `json:"name"`                // Default operator name
	PublicKey         string `json:"public_key"`          // Operator public key
	PrivateKey        string `json:"private_key"`         // Operator private key (plaintext)
	Seed              string `json:"seed"`                // Operator seed (plaintext)
	SigningPublicKey  string `json:"signing_public_key"`  // Operator signing public key
	SigningPrivateKey string `json:"signing_private_key"` // Operator signing private key (plaintext)
	SigningSeed       string `json:"signing_seed"`        // Operator signing seed (plaintext)
	JWT               string `json:"jwt"`                 // Generated operator JWT
	Created           time.Time `json:"created"`
	Updated           time.Time `json:"updated"`
}

// PublishQueueRecord represents a queued account publish operation
type PublishQueueRecord struct {
	ID        string     `json:"id"`
	AccountID string     `json:"account_id"` // FK to accounts
	Action    string     `json:"action"`     // "upsert" or "delete"
	Message   string     `json:"message"`    // Error message if failed
	Attempts  int        `json:"attempts"`   // Number of retry attempts
	FailedAt  *time.Time `json:"failed_at"`  // When the record was marked as permanently failed
	Created   time.Time  `json:"created"`
	Updated   time.Time  `json:"updated"`
}

// RetryConfig defines the retry and failover behavior for NATS connections
type RetryConfig struct {
	MaxPrimaryRetries int           `json:"max_primary_retries"` // Number of retries on primary before failover
	InitialBackoff    time.Duration `json:"initial_backoff"`     // Initial backoff duration
	MaxBackoff        time.Duration `json:"max_backoff"`         // Maximum backoff duration
	BackoffMultiplier float64       `json:"backoff_multiplier"`  // Backoff multiplier for exponential backoff
	FailbackInterval  time.Duration `json:"failback_interval"`   // How often to check primary health for failback
}

// DefaultRetryConfig returns sensible defaults for retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxPrimaryRetries: 4,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        8 * time.Second,
		BackoffMultiplier: 2.0,
		FailbackInterval:  30 * time.Second,
	}
}

// TimeoutConfig defines timeout settings for NATS connections
type TimeoutConfig struct {
	ConnectTimeout time.Duration `json:"connect_timeout"` // Connection establishment timeout
	PublishTimeout time.Duration `json:"publish_timeout"` // Publish operation timeout
	RequestTimeout time.Duration `json:"request_timeout"` // Request operation timeout
}

// DefaultTimeoutConfig returns sensible defaults for timeout configuration
func DefaultTimeoutConfig() *TimeoutConfig {
	return &TimeoutConfig{
		ConnectTimeout: 5 * time.Second,
		PublishTimeout: 10 * time.Second,
		RequestTimeout: 10 * time.Second,
	}
}

// Options allows customizing the behavior of the NATS JWT synchronization
type Options struct {
	// Collection names
	UserCollectionName    string
	RoleCollectionName    string
	AccountCollectionName string
	
	// NATS configuration  
	OperatorName              string
	NATSServerURL             string
	BackupNATSServerURLs      []string
	
	// Connection management
	ConnectionRetryConfig     *RetryConfig  // Retry and failover configuration
	ConnectionTimeouts        *TimeoutConfig // Connection timeout configuration
	
	// JWT settings
	DefaultJWTExpiry          time.Duration // Default: 0 (never expires)
	
	// Performance
	PublishQueueInterval time.Duration // How often to process the publish queue
	DebounceInterval     time.Duration // Wait time after changes before processing
	LogToConsole         bool
	
	// Failed record cleanup
	FailedRecordCleanupInterval time.Duration // How often to run cleanup job
	FailedRecordRetentionTime   time.Duration // How old records must be before deletion
	
	// Default permissions for users when role permissions are empty
	// Note: Accounts provide isolation boundaries, these are user defaults within accounts
	DefaultPublishPermissions   []string // Default publish permissions when role is empty
	DefaultSubscribePermissions []string // Default subscribe permissions when role is empty
	
	// Custom event filter function
	// Return true to process the event, false to ignore
	EventFilter func(collectionName, eventType string) bool
}

// Collection names with nats_ prefix
const (
	DefaultAccountCollectionName      = "nats_accounts"
	DefaultUserCollectionName         = "nats_users"
	DefaultRoleCollectionName         = "nats_roles"
	SystemOperatorCollectionName      = "nats_system_operator"
	PublishQueueCollectionName        = "nats_publish_queue"
)

// Default operator name
const (
	DefaultOperatorName = "stone-age.io"
)

// Publishing actions for the queue
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

// Default permissions without scoping (accounts provide isolation)
const (
	DefaultInboxSubscribe = "_INBOX.>" // Standard inbox pattern
)

// Default permission arrays for users when role permissions are empty
var DefaultPublishPermissions = []string{">"}                    // Full access within account
var DefaultSubscribePermissions = []string{">", "_INBOX.>"}      // Full access + inbox within account

// Timeout and retry constants to eliminate magic numbers
const (
	DefaultNATSTimeout        = 10 * time.Second
	DefaultNATSConnectTimeout = 5 * time.Second
	MaxQueueRetries           = 5
	DefaultJWTCacheSize       = 1000
	MaxQueueAttempts          = 10
	DefaultProcessingTimeout  = 30 * time.Second
)

// GetPublishPermissions extracts the string array from the JSON field
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

// GetSubscribePermissions extracts the string array from the JSON field
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

// NormalizeName creates a valid NATS account name from the account name
// Accounts provide isolation, so no additional scoping needed
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
