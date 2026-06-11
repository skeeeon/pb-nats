// Package utils provides utility functions for the pb-nats library
package utils

import (
	"fmt"
	"strings"
	"time"
)

// TruncateString limits string length for logging and display purposes,
// appending "..." when the input is shortened. Prevents log spam from
// large JWTs and data structures.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	if maxLen <= 3 {
		return s[:maxLen]
	}

	return s[:maxLen-3] + "..."
}

// ValidateRequired ensures string fields are not empty after trimming whitespace.
func ValidateRequired(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required and cannot be empty", fieldName)
	}
	return nil
}

// ValidatePositiveDuration ensures duration values are greater than zero.
func ValidatePositiveDuration(d time.Duration, fieldName string) error {
	if d <= 0 {
		return fmt.Errorf("%s must be positive, got: %v", fieldName, d)
	}
	return nil
}

// ValidateURL performs basic URL format validation for NATS server URLs.
// Checks protocol prefix only; connectivity is handled by the connection manager.
func ValidateURL(url, fieldName string) error {
	if url == "" {
		return fmt.Errorf("%s cannot be empty", fieldName)
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") &&
		!strings.HasPrefix(url, "nats://") && !strings.HasPrefix(url, "tls://") {
		return fmt.Errorf("%s must include a valid protocol (http://, https://, nats://, tls://)", fieldName)
	}

	return nil
}

// WrapError adds context to an error while preserving the original for unwrapping.
// Returns nil if err is nil.
func WrapError(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

// WrapErrorf adds printf-formatted context to an error while preserving the
// original for unwrapping. Returns nil if err is nil.
func WrapErrorf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	context := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: %w", context, err)
}
