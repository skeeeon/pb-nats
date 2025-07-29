package pbnats

import (
	"errors"
	"github.com/skeeeon/pb-nats/internal/utils"
)

// Common errors returned by the library
// Organized by category for better maintainability
var (
	// Collection errors
	ErrCollectionNotFound = errors.New("collection not found")
	ErrRecordNotFound     = errors.New("record not found")
	ErrInvalidRecord      = errors.New("invalid record data")

	// Key generation errors
	ErrKeyGeneration   = errors.New("failed to generate keys")
	ErrInvalidSeed     = errors.New("invalid seed provided")
	ErrInvalidKeyPair  = errors.New("invalid key pair")

	// JWT errors
	ErrJWTGeneration = errors.New("failed to generate JWT")
	ErrJWTInvalid    = errors.New("invalid JWT")
	ErrJWTExpired    = errors.New("JWT expired")

	// Account errors (updated terminology)
	ErrAccountNotFound        = errors.New("account not found")
	ErrAccountInactive        = errors.New("account is inactive")
	ErrSystemAccountProtected = errors.New("cannot modify system account")

	// User errors
	ErrUserNotFound         = errors.New("user not found")
	ErrUserInactive         = errors.New("user is inactive")
	ErrUserAlreadyExists    = errors.New("user already exists")
	ErrUserNotAuthorized    = errors.New("user not authorized")

	// Role errors
	ErrRoleNotFound      = errors.New("role not found")
	ErrInvalidPermission = errors.New("invalid permission")

	// Publishing errors
	ErrNATSConnection    = errors.New("failed to connect to NATS")
	ErrPublishFailed     = errors.New("failed to publish to NATS")
	ErrQueueFull         = errors.New("publish queue is full")

	// Configuration errors
	ErrInvalidOptions       = errors.New("invalid options provided")
	ErrMissingOperator      = errors.New("system operator not found")
	ErrMissingSystemAccount = errors.New("system account not found")
)

// Error classification and handling functions

// IsTemporaryError checks if an error is temporary and should be retried
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

// IsPermanentError checks if an error is permanent and should not be retried
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

// IsConfigurationError checks if an error is related to configuration
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

// Error wrapping helpers for consistent error handling

// WrapCollectionError wraps errors related to collection operations
func WrapCollectionError(err error, collectionName, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "collection %q %s operation failed", collectionName, operation)
}

// WrapRecordError wraps errors related to record operations
func WrapRecordError(err error, recordID, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "record %q %s operation failed", recordID, operation)
}

// WrapJWTError wraps errors related to JWT operations
func WrapJWTError(err error, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "JWT %s operation failed", operation)
}

// WrapNATSError wraps errors related to NATS operations
func WrapNATSError(err error, operation string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "NATS %s operation failed", operation)
}

// WrapInitializationError wraps errors that occur during system initialization
func WrapInitializationError(err error, component string) error {
	if err == nil {
		return nil
	}
	return utils.WrapErrorf(err, "failed to initialize %s", component)
}

// Error context helpers

// ErrorContext provides additional context for errors
type ErrorContext struct {
	Component   string                 `json:"component,omitempty"`
	Operation   string                 `json:"operation,omitempty"`
	ResourceID  string                 `json:"resource_id,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
	Temporary   bool                   `json:"temporary"`
	Permanent   bool                   `json:"permanent"`
	Retryable   bool                   `json:"retryable"`
}

// NewErrorContext creates a new error context with classification
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

// WithResourceID adds a resource ID to the error context
func (ec *ErrorContext) WithResourceID(id string) *ErrorContext {
	if ec != nil {
		ec.ResourceID = id
	}
	return ec
}

// WithDetail adds a detail to the error context
func (ec *ErrorContext) WithDetail(key string, value interface{}) *ErrorContext {
	if ec != nil {
		if ec.Details == nil {
			ec.Details = make(map[string]interface{})
		}
		ec.Details[key] = value
	}
	return ec
}

// ShouldRetry returns whether the error should be retried
func (ec *ErrorContext) ShouldRetry() bool {
	return ec != nil && ec.Retryable
}

// Error severity levels for logging and monitoring

// ErrorSeverity represents the severity of an error
type ErrorSeverity int

const (
	SeverityLow ErrorSeverity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// String returns the string representation of the severity
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

// GetErrorSeverity determines the severity of an error
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
