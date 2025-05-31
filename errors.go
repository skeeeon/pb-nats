package pbnats

import "errors"

// Common errors returned by the library
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

	// Organization errors
	ErrOrganizationNotFound   = errors.New("organization not found")
	ErrOrganizationInactive   = errors.New("organization is inactive")
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
	ErrInvalidOptions     = errors.New("invalid options provided")
	ErrMissingOperator    = errors.New("system operator not found")
	ErrMissingSystemAccount = errors.New("system account not found")
)

// IsTemporaryError checks if an error is temporary and should be retried
func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}

	// Network and connection errors are usually temporary
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
	}

	for _, pattern := range temporaryPatterns {
		if contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// contains is a simple string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		(len(s) > len(substr) && s[len(s)-len(substr):] == substr) ||
		indexOf(s, substr) >= 0)
}

// indexOf finds the index of substr in s, returns -1 if not found
func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(s) < len(substr) {
		return -1
	}
	
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
