package pbnats

import (
	"errors"
	"strings"
)

// Common errors returned by the library organized by operational category.
var (
	// Collection errors - Database and schema related issues
	ErrCollectionNotFound = errors.New("collection not found") // PocketBase collection missing
	ErrRecordNotFound     = errors.New("record not found")     // Database record missing
	ErrInvalidRecord      = errors.New("invalid record data")  // Record validation failed

	// Key generation errors - Cryptographic operations
	ErrKeyGeneration  = errors.New("failed to generate keys") // NKey generation failed
	ErrInvalidSeed    = errors.New("invalid seed provided")   // Malformed seed data
	ErrInvalidKeyPair = errors.New("invalid key pair")        // Key pair validation failed

	// JWT errors - Token generation and validation
	ErrJWTGeneration = errors.New("failed to generate JWT") // JWT creation failed
	ErrJWTInvalid    = errors.New("invalid JWT")            // JWT format or signature invalid
	ErrJWTExpired    = errors.New("JWT expired")            // JWT past expiration time

	// Account errors - NATS account management
	ErrAccountNotFound        = errors.New("account not found")            // Account record missing
	ErrAccountInactive        = errors.New("account is inactive")          // Account disabled
	ErrSystemAccountProtected = errors.New("cannot modify system account") // System account protection

	// User errors - NATS user management
	ErrUserNotFound      = errors.New("user not found")      // User record missing
	ErrUserInactive      = errors.New("user is inactive")    // User disabled
	ErrUserAlreadyExists = errors.New("user already exists") // Duplicate user creation
	ErrUserNotAuthorized = errors.New("user not authorized") // Permission denied

	// Role errors - Permission management
	ErrRoleNotFound      = errors.New("role not found")     // Role record missing
	ErrInvalidPermission = errors.New("invalid permission") // Permission format invalid

	// Publishing errors - NATS communication
	ErrNATSConnection = errors.New("failed to connect to NATS") // NATS connectivity issues
	ErrPublishFailed  = errors.New("failed to publish to NATS") // NATS publish operation failed
	ErrQueueFull      = errors.New("publish queue is full")     // Queue capacity exceeded

	// Configuration errors - System setup issues
	ErrInvalidOptions       = errors.New("invalid options provided")  // Configuration validation failed
	ErrMissingOperator      = errors.New("system operator not found") // Operator initialization missing
	ErrMissingSystemAccount = errors.New("system account not found")  // System account missing
)

// IsTemporaryError determines if an error represents a temporary condition that
// should be retried (network connectivity, timeouts, NATS unavailability).
func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrNATSConnection) || errors.Is(err, ErrPublishFailed) {
		return true
	}

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
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// IsPermanentError determines if an error represents a permanent condition that
// should not be retried (invalid data, authorization failures, configuration issues).
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}

	permanentErrors := []error{
		ErrInvalidRecord, ErrInvalidSeed, ErrInvalidKeyPair, ErrJWTInvalid,
		ErrSystemAccountProtected, ErrUserNotAuthorized, ErrInvalidPermission,
		ErrInvalidOptions,
	}
	for _, target := range permanentErrors {
		if errors.Is(err, target) {
			return true
		}
	}

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
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}
