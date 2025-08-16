package pbnats

import (
	"errors"
	"github.com/skeeeon/pb-nats/internal/utils"
)

// Common errors returned by the library organized by operational category.
// This error taxonomy enables consistent error handling across all components
// and supports intelligent retry logic based on error classification.
//
// ERROR CLASSIFICATION PHILOSOPHY:
// Errors are classified into categories that indicate appropriate handling:
// - Permanent errors: Don't retry, fix configuration or data
// - Temporary errors: Retry with backoff, likely network/timing issues
// - Configuration errors: System setup problems requiring admin intervention
//
// COMPONENT ORGANIZATION:
// Errors are grouped by the component or operation that generates them,
// making it easier to identify error sources and implement targeted fixes.
var (
	// Collection errors - Database and schema related issues
	ErrCollectionNotFound = errors.New("collection not found")    // PocketBase collection missing
	ErrRecordNotFound     = errors.New("record not found")        // Database record missing
	ErrInvalidRecord      = errors.New("invalid record data")     // Record validation failed

	// Key generation errors - Cryptographic operations
	ErrKeyGeneration   = errors.New("failed to generate keys")    // NKey generation failed
	ErrInvalidSeed     = errors.New("invalid seed provided")      // Malformed seed data
	ErrInvalidKeyPair  = errors.New("invalid key pair")           // Key pair validation failed

	// JWT errors - Token generation and validation
	ErrJWTGeneration = errors.New("failed to generate JWT")       // JWT creation failed
	ErrJWTInvalid    = errors.New("invalid JWT")                  // JWT format or signature invalid
	ErrJWTExpired    = errors.New("JWT expired")                  // JWT past expiration time

	// Account errors - NATS account management (updated terminology)
	ErrAccountNotFound        = errors.New("account not found")         // Account record missing
	ErrAccountInactive        = errors.New("account is inactive")       // Account disabled
	ErrSystemAccountProtected = errors.New("cannot modify system account") // System account protection

	// User errors - NATS user management
	ErrUserNotFound         = errors.New("user not found")         // User record missing
	ErrUserInactive         = errors.New("user is inactive")       // User disabled  
	ErrUserAlreadyExists    = errors.New("user already exists")    // Duplicate user creation
	ErrUserNotAuthorized    = errors.New("user not authorized")    // Permission denied

	// Role errors - Permission management
	ErrRoleNotFound      = errors.New("role not found")        // Role record missing
	ErrInvalidPermission = errors.New("invalid permission")    // Permission format invalid

	// Publishing errors - NATS communication
	ErrNATSConnection    = errors.New("failed to connect to NATS")  // NATS connectivity issues
	ErrPublishFailed     = errors.New("failed to publish to NATS")  // NATS publish operation failed
	ErrQueueFull         = errors.New("publish queue is full")      // Queue capacity exceeded

	// Configuration errors - System setup issues
	ErrInvalidOptions       = errors.New("invalid options provided")     // Configuration validation failed
	ErrMissingOperator      = errors.New("system operator not found")    // Operator initialization missing
	ErrMissingSystemAccount = errors.New("system account not found")     // System account missing
)

// Error classification functions for intelligent handling and retry logic

// IsTemporaryError determines if an error represents a temporary condition that should be retried.
// This classification drives the retry logic throughout the system.
//
// TEMPORARY ERROR CHARACTERISTICS:
// - Network connectivity issues (likely to resolve)
// - Resource temporarily unavailable
// - Timeout conditions
// - NATS server temporary unavailability
//
// RETRY STRATEGY:
// Temporary errors trigger exponential backoff retry logic in:
// - Connection manager failover
// - Queue processor retry attempts
// - Bootstrap mode connection attempts
//
// PARAMETERS:
//   - err: Error to classify
//
// RETURNS:
//   - true: Error is temporary, retry appropriate
//   - false: Error is permanent or classification uncertain
//
// CLASSIFICATION LOGIC:
// Uses both exact error matching and string pattern matching
// to catch various network-related error conditions.
func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}

	// Known temporary errors
	switch err {
	case ErrNATSConnection, ErrPublishFailed:
		return true
	}

	// String-based checks for common temporary error patterns
	errStr := err.Error()
	temporaryPatterns := []string{
		"connection refused",
		"timeout",
		"temporary failure",
		"network is unreachable",
		"no route to host",
		"connection reset",
		"connection timed out",
	}

	for _, pattern := range temporaryPatterns {
		if utils.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// IsPermanentError determines if an error represents a permanent condition that should not be retried.
// This classification prevents wasted retry attempts on errors requiring manual intervention.
//
// PERMANENT ERROR CHARACTERISTICS:
// - Invalid configuration or data
// - Authentication/authorization failures
// - Malformed requests or parameters
// - Business logic violations
//
// HANDLING STRATEGY:
// Permanent errors should:
// - Stop retry attempts immediately
// - Log detailed error information
// - Alert administrators for manual resolution
// - Update queue records as permanently failed
//
// PARAMETERS:
//   - err: Error to classify
//
// RETURNS:
//   - true: Error is permanent, don't retry
//   - false: Error may be temporary or classification uncertain
//
// CLASSIFICATION LOGIC:
// Uses both exact error matching and string pattern matching
// to identify validation and authorization failures.
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}

	// Known permanent errors
	switch err {
	case ErrInvalidRecord, ErrInvalidSeed, ErrInvalidKeyPair, ErrJWTInvalid,
		 ErrSystemAccountProtected, ErrUserNotAuthorized, ErrInvalidPermission,
		 ErrInvalidOptions:
		return true
	}

	// String-based checks for permanent error patterns
	errStr := err.Error()
	permanentPatterns := []string{
		"invalid",
		"forbidden",
		"unauthorized",
		"bad request",
		"not found",
		"already exists",
	}

	for _, pattern := range permanentPatterns {
		if utils.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// IsConfigurationError determines if an error is related to system configuration.
// These errors require administrative intervention to resolve.
//
// CONFIGURATION ERROR CHARACTERISTICS:
// - Missing required system components
// - Invalid configuration parameters
// - System initialization failures
// - Missing environment variables or files
//
// OPERATIONAL RESPONSE:
// Configuration errors should:
// - Prevent system startup (fail-fast)
// - Provide clear remediation instructions
// - Be logged at critical severity level
// - Not trigger retry attempts
//
// PARAMETERS:
//   - err: Error to classify
//
// RETURNS:
//   - true: Error is configuration-related
//   - false: Error is operational or classification uncertain
//
// USAGE CONTEXT:
// Used during system initialization to distinguish between
// configuration issues and operational failures.
func IsConfigurationError(err error) bool {
	if err == nil {
		return false
	}

	switch err {
	case ErrInvalidOptions, ErrMissingOperator, ErrMissingSystemAccount:
		return true
	}

	errStr := err.Error()
	configPatterns := []string{
		"configuration",
		"invalid options",
		"missing",
		"not configured",
	}

	for _, pattern := range configPatterns {
		if utils.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// Error wrapping functions for consistent error context and debugging

// WrapCollectionError adds context for collection-related operations.
// This provides consistent error messaging for database operations.
//
// CONTEXT ENHANCEMENT:
// Transforms generic database errors into specific collection operation errors
// with clear identification of the collection and operation involved.
//
// PARAMETERS:
//   - err: Original error from database operation
//   - collectionName: Name of PocketBase collection involved
//   - operation: Type of operation (create, read, update, delete)
//
// RETURNS:
//   - nil: If original error was nil
//   - error: Enhanced error with collection context
//
// USAGE PATTERN:
//   if err := app.Save(record); err != nil {
//       return WrapCollectionError(err, "nats_users", "create")
//   }
func WrapCollectionError(err error, collectionName, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "collection %q %s operation failed", collectionName, operation)
}

// WrapRecordError adds context for record-specific operations.
// This provides clear identification of which record operation failed.
//
// DEBUGGING ENHANCEMENT:
// Record-level errors can be difficult to trace without knowing
// which specific record was involved in the operation.
//
// PARAMETERS:
//   - err: Original error from record operation
//   - recordID: Database ID of the record involved
//   - operation: Type of operation performed
//
// RETURNS:
//   - nil: If original error was nil
//   - error: Enhanced error with record context
//
// USAGE PATTERN:
//   if err := processUser(userID); err != nil {
//       return WrapRecordError(err, userID, "JWT generation")
//   }
func WrapRecordError(err error, recordID, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "record %q %s operation failed", recordID, operation)
}

// WrapJWTError adds context for JWT-related operations.
// JWT operations can fail at multiple stages, requiring clear context.
//
// JWT OPERATION TYPES:
// - Generation: Creating new JWTs from claims
// - Validation: Verifying JWT signatures and structure
// - Encoding: Converting claims to JWT format
// - Parsing: Extracting claims from JWT strings
//
// PARAMETERS:
//   - err: Original error from JWT operation
//   - operation: Type of JWT operation performed
//
// RETURNS:
//   - nil: If original error was nil
//   - error: Enhanced error with JWT operation context
//
// USAGE PATTERN:
//   if jwtValue, err := claims.Encode(keyPair); err != nil {
//       return WrapJWTError(err, "encoding")
//   }
func WrapJWTError(err error, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "JWT %s operation failed", operation)
}

// WrapNATSError adds context for NATS communication operations.
// NATS operations involve network communication with various failure modes.
//
// NATS OPERATION TYPES:
// - Connection: Establishing NATS server connections
// - Publishing: Sending messages to NATS subjects
// - Requesting: Request-response pattern operations
// - Subscription: Setting up message handlers
//
// PARAMETERS:
//   - err: Original error from NATS operation
//   - operation: Type of NATS operation performed
//
// RETURNS:
//   - nil: If original error was nil
//   - error: Enhanced error with NATS operation context
//
// USAGE PATTERN:
//   if _, err := conn.Request(subject, data, timeout); err != nil {
//       return WrapNATSError(err, "account JWT publish")
//   }
func WrapNATSError(err error, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "NATS %s operation failed", operation)
}

// WrapInitializationError adds context for system startup operations.
// System initialization involves multiple components that must start in sequence.
//
// INITIALIZATION PHASES:
// - Collection creation
// - System component setup (operator, account, user)
// - Connection establishment
// - Hook registration
//
// PARAMETERS:
//   - err: Original error from initialization operation
//   - component: Name of component being initialized
//
// RETURNS:
//   - nil: If original error was nil
//   - error: Enhanced error with initialization context
//
// USAGE PATTERN:
//   if err := collectionManager.InitializeCollections(); err != nil {
//       return WrapInitializationError(err, "collections")
//   }
func WrapInitializationError(err error, component string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "failed to initialize %s", component)
}

// Advanced error context and metadata for monitoring and debugging

// ErrorContext provides structured error information for logging and monitoring.
// This enables sophisticated error handling and alerting based on error characteristics.
//
// MONITORING INTEGRATION:
// Error contexts can be serialized to JSON for monitoring systems,
// enabling automated alerting based on error patterns and severity.
//
// RETRY DECISION SUPPORT:
// The context includes computed flags (Temporary, Permanent, Retryable)
// that support automated retry decision making.
type ErrorContext struct {
	Component   string                 `json:"component,omitempty"`   // Component where error occurred
	Operation   string                 `json:"operation,omitempty"`   // Operation being performed
	ResourceID  string                 `json:"resource_id,omitempty"` // ID of resource involved
	Details     map[string]interface{} `json:"details,omitempty"`     // Additional error metadata
	Temporary   bool                   `json:"temporary"`             // Is error temporary
	Permanent   bool                   `json:"permanent"`             // Is error permanent
	Retryable   bool                   `json:"retryable"`             // Should operation be retried
}

// NewErrorContext creates error context with automatic error classification.
// This provides a structured way to capture error metadata for monitoring.
//
// AUTOMATIC CLASSIFICATION:
// The context automatically classifies the error using the classification
// functions and computes the retryable flag based on the results.
//
// PARAMETERS:
//   - err: Original error to analyze
//   - component: Component name where error occurred
//   - operation: Operation name being performed
//
// RETURNS:
//   - *ErrorContext: Structured error context with classifications
//   - nil: If err parameter was nil
//
// USAGE PATTERN:
//   ctx := NewErrorContext(err, "publisher", "account_publish")
//   if ctx != nil && ctx.ShouldRetry() {
//       // Implement retry logic
//   }
func NewErrorContext(err error, component, operation string) *ErrorContext {
	if err == nil {
		return nil
	}

	temporary := IsTemporaryError(err)
	permanent := IsPermanentError(err)
	
	return &ErrorContext{
		Component: component,
		Operation: operation,
		Temporary: temporary,
		Permanent: permanent,
		Retryable: temporary && !permanent,
	}
}

// WithResourceID adds a resource identifier to the error context.
// This helps identify which specific resource was involved in the error.
//
// RESOURCE IDENTIFICATION:
// Resource IDs help correlate errors with specific database records,
// NATS accounts, or other system entities for debugging.
//
// PARAMETERS:
//   - id: Resource identifier (database ID, account name, etc.)
//
// RETURNS:
//   - *ErrorContext: Context with resource ID added (chainable)
//
// USAGE PATTERN:
//   ctx := NewErrorContext(err, "sync", "user_update").WithResourceID(userID)
func (ec *ErrorContext) WithResourceID(id string) *ErrorContext {
	if ec != nil {
		ec.ResourceID = id
	}
	return ec
}

// WithDetail adds arbitrary metadata to the error context.
// This enables capture of additional debugging information specific to the error.
//
// DEBUGGING METADATA:
// Details can include configuration values, intermediate results,
// or any other information that might help diagnose the error.
//
// PARAMETERS:
//   - key: Metadata field name
//   - value: Metadata field value (will be JSON serialized)
//
// RETURNS:
//   - *ErrorContext: Context with detail added (chainable)
//
// USAGE PATTERN:
//   ctx := NewErrorContext(err, "connection", "failover").
//       WithDetail("primary_url", primaryURL).
//       WithDetail("attempt_count", attempts)
func (ec *ErrorContext) WithDetail(key string, value interface{}) *ErrorContext {
	if ec != nil {
		if ec.Details == nil {
			ec.Details = make(map[string]interface{})
		}
		ec.Details[key] = value
	}
	return ec
}

// ShouldRetry determines if the operation should be retried based on error classification.
// This provides a consistent retry decision across all system components.
//
// RETRY LOGIC:
// - Temporary && !Permanent = Retryable
// - Temporary errors that are also classified as permanent are not retried
// - This handles edge cases where classification might overlap
//
// RETURNS:
//   - true: Operation should be retried with appropriate backoff
//   - false: Operation should not be retried
//
// USAGE PATTERN:
//   if ctx := NewErrorContext(err, "publisher", "publish"); ctx.ShouldRetry() {
//       return scheduleRetry(operation)
//   }
func (ec *ErrorContext) ShouldRetry() bool {
	return ec != nil && ec.Retryable
}

// Error severity classification for monitoring and alerting

// ErrorSeverity represents the operational impact level of an error.
// This enables appropriate alerting and response escalation.
//
// SEVERITY LEVELS:
// - Low: Minor issues, self-healing, informational
// - Medium: Operational issues, degraded performance
// - High: Service impact, manual intervention may be needed
// - Critical: System failure, immediate intervention required
type ErrorSeverity int

const (
	SeverityLow ErrorSeverity = iota      // Minor issues, self-healing
	SeverityMedium                        // Operational issues requiring attention
	SeverityHigh                          // Service impact requiring intervention
	SeverityCritical                      // System failure requiring immediate action
)

// String returns human-readable severity level for logging and monitoring.
//
// RETURNS:
//   - string: Lowercase severity name for consistent logging
func (s ErrorSeverity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// GetErrorSeverity determines the operational severity of an error.
// This classification drives alerting and escalation procedures.
//
// SEVERITY CLASSIFICATION RULES:
// - Critical: System initialization failures, core component failures
// - High: NATS connectivity loss, authentication failures
// - Medium: JWT generation issues, individual record failures
// - Low: Default classification for unspecified errors
//
// ALERTING INTEGRATION:
// Monitoring systems can use severity levels to:
// - Route critical errors to on-call engineers
// - Create tickets for high severity issues
// - Log medium/low severity for trending analysis
//
// PARAMETERS:
//   - err: Error to classify for severity
//
// RETURNS:
//   - ErrorSeverity: Operational impact level of the error
//
// CLASSIFICATION LOGIC:
// Uses both exact error matching and pattern matching to classify
// errors based on their potential operational impact.
func GetErrorSeverity(err error) ErrorSeverity {
	if err == nil {
		return SeverityLow
	}

	// Critical errors that affect core functionality
	switch err {
	case ErrMissingOperator, ErrMissingSystemAccount, ErrInvalidOptions:
		return SeverityCritical
	}

	// High severity errors
	switch err {
	case ErrNATSConnection, ErrPublishFailed, ErrSystemAccountProtected:
		return SeverityHigh
	}

	// Medium severity errors
	switch err {
	case ErrJWTGeneration, ErrJWTInvalid, ErrKeyGeneration:
		return SeverityMedium
	}

	// Check for critical patterns in error messages
	errStr := err.Error()
	criticalPatterns := []string{
		"system",
		"operator",
		"initialization",
		"bootstrap",
	}

	for _, pattern := range criticalPatterns {
		if utils.Contains(errStr, pattern) {
			return SeverityCritical
		}
	}

	// Default to low severity
	return SeverityLow
}
