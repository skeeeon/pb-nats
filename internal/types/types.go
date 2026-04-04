// Package types defines all shared types used throughout the pb-nats library
package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SigningKeyPublic is the API-visible portion of a signing key.
// Contains only public key and metadata — safe to expose via REST API.
type SigningKeyPublic struct {
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
}

// SigningKeyPrivate contains the full key material for JWT signing.
// This data is stored in a hidden field and never exposed via API.
type SigningKeyPrivate struct {
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key"`
	Seed      string    `json:"seed"`
	CreatedAt time.Time `json:"created_at"`
}

// ParseSigningKeysPublic parses a JSON array of public signing keys.
func ParseSigningKeysPublic(data json.RawMessage) ([]SigningKeyPublic, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var keys []SigningKeyPublic
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// ParseSigningKeysPrivate parses a JSON array of private signing keys.
func ParseSigningKeysPrivate(data json.RawMessage) ([]SigningKeyPrivate, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var keys []SigningKeyPrivate
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

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
// - Signing keys: Multiple signing keys supported, all embedded in account JWT
// - The most recently added key (last in array) signs new user JWTs
// - JWT: Account definition published to NATS server
//
// SIGNING KEY MANAGEMENT:
// - add_signing_key: Generates and appends a new signing key (graceful rotation)
// - remove_signing_key: Removes a specific key by public key
// - rotate_keys: Emergency rotation — purges all keys, generates single new one
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
	SigningKeys        []SigningKeyPublic  `json:"signing_keys"`
	SigningKeysPrivate []SigningKeyPrivate `json:"signing_keys_private"`
	JWT               string              `json:"jwt"`
	Active            bool                `json:"active"`
	RotateKeys        bool                `json:"rotate_keys"`
	
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

	// Per-user permission overrides (merged with role permissions via union)
	PublishPermissions       json.RawMessage `json:"publish_permissions"`
	SubscribePermissions     json.RawMessage `json:"subscribe_permissions"`
	PublishDenyPermissions   json.RawMessage `json:"publish_deny_permissions"`
	SubscribeDenyPermissions json.RawMessage `json:"subscribe_deny_permissions"`
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
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	PublicKey          string              `json:"public_key"`
	PrivateKey         string              `json:"private_key"`
	Seed               string              `json:"seed"`
	SigningKeys        []SigningKeyPublic  `json:"signing_keys"`
	SigningKeysPrivate []SigningKeyPrivate `json:"signing_keys_private"`
	JWT                string              `json:"jwt"`
	SystemAccountID    string              `json:"system_account_id"`
	Created            time.Time           `json:"created"`
	Updated            time.Time           `json:"updated"`
}

// LatestSigningKey returns the most recently added signing key (last in array).
// This is the key used to sign new account JWTs.
func (o *SystemOperatorRecord) LatestSigningKey() *SigningKeyPrivate {
	if len(o.SigningKeysPrivate) == 0 {
		return nil
	}
	return &o.SigningKeysPrivate[len(o.SigningKeysPrivate)-1]
}

// AllSigningPublicKeys returns all signing public keys for JWT embedding.
func (o *SystemOperatorRecord) AllSigningPublicKeys() []string {
	keys := make([]string, len(o.SigningKeys))
	for i, k := range o.SigningKeys {
		keys[i] = k.PublicKey
	}
	return keys
}

// LatestSigningKey returns the most recently added signing key (last in array).
// This is the key used to sign new user JWTs within this account.
func (a *AccountRecord) LatestSigningKey() *SigningKeyPrivate {
	if len(a.SigningKeysPrivate) == 0 {
		return nil
	}
	return &a.SigningKeysPrivate[len(a.SigningKeysPrivate)-1]
}

// AllSigningPublicKeys returns all signing public keys for JWT embedding.
func (a *AccountRecord) AllSigningPublicKeys() []string {
	keys := make([]string, len(a.SigningKeys))
	for i, k := range a.SigningKeys {
		keys[i] = k.PublicKey
	}
	return keys
}

// NewSigningKeyPair creates a matched pair of public and private signing key entries.
func NewSigningKeyPair(publicKey, privateKey, seed string) (SigningKeyPublic, SigningKeyPrivate) {
	now := time.Now()
	return SigningKeyPublic{
			PublicKey: publicKey,
			CreatedAt: now,
		}, SigningKeyPrivate{
			PublicKey:  publicKey,
			PrivateKey: privateKey,
			Seed:      seed,
			CreatedAt: now,
		}
}

// MarshalSigningKeys marshals signing key slices to JSON for record storage.
func MarshalSigningKeys(pub []SigningKeyPublic, priv []SigningKeyPrivate) (json.RawMessage, json.RawMessage, error) {
	pubJSON, err := json.Marshal(pub)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal public signing keys: %w", err)
	}
	privJSON, err := json.Marshal(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private signing keys: %w", err)
	}
	return pubJSON, privJSON, nil
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

// AccountExportRecord represents a NATS account export for cross-account communication.
// Exports declare subjects that other accounts can import.
//
// EXPORT TYPES:
// - Stream: Continuous data flow — exporter publishes, importer subscribes
// - Service: Request-reply — importer sends requests, exporter responds
//
// TOKEN REQUIREMENT:
// When token_req is true, importing accounts must present an activation token
// (JWT) to access this export. This enables fine-grained access control.
type AccountExportRecord struct {
	ID                   string    `json:"id"`
	AccountID            string    `json:"account_id"`
	Name                 string    `json:"name"`
	Subject              string    `json:"subject"`
	Type                 string    `json:"type"`                  // "stream" or "service"
	TokenReq             bool      `json:"token_req"`
	ResponseType         string    `json:"response_type"`         // "Singleton", "Stream", "Chunked" (service only)
	ResponseThreshold    int64     `json:"response_threshold"`    // milliseconds (service only)
	AccountTokenPosition int       `json:"account_token_position"`
	Advertise            bool      `json:"advertise"`
	AllowTrace           bool      `json:"allow_trace"`
	Description          string    `json:"description"`
	Created              time.Time `json:"created"`
	Updated              time.Time `json:"updated"`
}

// AccountImportRecord represents a NATS account import for cross-account communication.
// Imports consume subjects exported by other accounts.
//
// IMPORT TYPES:
// - Stream: Subscribe to data published by the exporting account
// - Service: Send requests to a service provided by the exporting account
//
// LOCAL SUBJECT REMAPPING:
// LocalSubject allows remapping the imported subject to a different local subject.
// Supports positional references ($1, $2) for wildcard subjects.
type AccountImportRecord struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	Name         string    `json:"name"`
	Subject      string    `json:"subject"`
	Account      string    `json:"account"`       // public key of exporting account
	Token        string    `json:"token"`          // optional activation JWT
	LocalSubject string    `json:"local_subject"`
	Type         string    `json:"type"`           // "stream" or "service"
	Share        bool      `json:"share"`
	AllowTrace   bool      `json:"allow_trace"`
	Description  string    `json:"description"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
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
	UserCollectionName      string
	RoleCollectionName      string
	AccountCollectionName   string
	ExportCollectionName    string
	ImportCollectionName    string
	
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

	// EncryptionKey enables AES-256-GCM at-rest encryption of sensitive fields
	// (private keys, seeds, signing keys) when set. Must be exactly 32 characters.
	// Empty string disables encryption.
	EncryptionKey string
}

// Collection names with nats_ prefix for clear identification
const (
	DefaultAccountCollectionName = "nats_accounts"
	DefaultUserCollectionName    = "nats_users"
	DefaultRoleCollectionName    = "nats_roles"
	DefaultExportCollectionName  = "nats_account_exports"
	DefaultImportCollectionName  = "nats_account_imports"
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

// GetPublishPermissions extracts publish allow permissions from user's JSON field.
func (u *NatsUserRecord) GetPublishPermissions() ([]string, error) {
	return parseJSONPermissions(u.PublishPermissions)
}

// GetSubscribePermissions extracts subscribe allow permissions from user's JSON field.
func (u *NatsUserRecord) GetSubscribePermissions() ([]string, error) {
	return parseJSONPermissions(u.SubscribePermissions)
}

// GetPublishDenyPermissions extracts publish deny permissions from user's JSON field.
func (u *NatsUserRecord) GetPublishDenyPermissions() ([]string, error) {
	return parseJSONPermissions(u.PublishDenyPermissions)
}

// GetSubscribeDenyPermissions extracts subscribe deny permissions from user's JSON field.
func (u *NatsUserRecord) GetSubscribeDenyPermissions() ([]string, error) {
	return parseJSONPermissions(u.SubscribeDenyPermissions)
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
