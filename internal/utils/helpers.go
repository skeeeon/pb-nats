// Package utils provides utility functions for the pb-nats library
package utils

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// String utility functions
// =====================

// Contains checks if a string contains a substring
func Contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		IndexOf(s, substr) >= 0)
}

// IndexOf finds the index of substr in s, returns -1 if not found
func IndexOf(s, substr string) int {
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

// NormalizeIdentifier normalizes a string to be used as an identifier
// Converts to lowercase, replaces spaces and special chars with underscores
func NormalizeIdentifier(input string) string {
	if input == "" {
		return ""
	}
	
	// Convert to lowercase
	normalized := strings.ToLower(input)
	
	// Replace spaces and hyphens with underscores
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	
	// Remove any characters that aren't alphanumeric or underscore
	reg := regexp.MustCompile(`[^a-z0-9_]`)
	normalized = reg.ReplaceAllString(normalized, "")
	
	// Remove multiple consecutive underscores
	reg = regexp.MustCompile(`_+`)
	normalized = reg.ReplaceAllString(normalized, "_")
	
	// Trim underscores from start and end
	normalized = strings.Trim(normalized, "_")
	
	// Ensure it's not empty
	if normalized == "" {
		return "unnamed"
	}
	
	return normalized
}

// TruncateString truncates a string to a maximum length
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	
	if maxLen <= 3 {
		return s[:maxLen]
	}
	
	return s[:maxLen-3] + "..."
}

// UniqueStrings removes duplicates from a string slice
func UniqueStrings(slice []string) []string {
	if len(slice) <= 1 {
		return slice
	}
	
	seen := make(map[string]bool)
	result := make([]string, 0, len(slice))
	
	for _, str := range slice {
		if !seen[str] {
			seen[str] = true
			result = append(result, str)
		}
	}
	
	return result
}

// FilterEmptyStrings removes empty strings from a slice
func FilterEmptyStrings(slice []string) []string {
	result := make([]string, 0, len(slice))
	for _, str := range slice {
		if strings.TrimSpace(str) != "" {
			result = append(result, str)
		}
	}
	return result
}

// JSON utility functions
// =====================

// ParseJSONStringArray parses a JSON string into a string array
func ParseJSONStringArray(jsonStr string) ([]string, error) {
	if jsonStr == "" {
		return []string{}, nil
	}
	
	var result []string
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON array from %q: %w", TruncateString(jsonStr, 50), err)
	}
	
	return result, nil
}

// StringArrayToJSON converts a string array to JSON
func StringArrayToJSON(arr []string) (string, error) {
	if len(arr) == 0 {
		return "[]", nil
	}
	
	jsonBytes, err := json.Marshal(arr)
	if err != nil {
		return "", fmt.Errorf("failed to marshal array to JSON: %w", err)
	}
	
	return string(jsonBytes), nil
}

// NATS utility functions
// =====================

// ApplySubjectScoping applies organization and user scoping to subject patterns
func ApplySubjectScoping(subjects []string, orgName, username string) []string {
	if len(subjects) == 0 {
		return subjects
	}
	
	result := make([]string, len(subjects))
	for i, subject := range subjects {
		// Replace organization placeholder
		scoped := strings.ReplaceAll(subject, "{org}", orgName)
		
		// Replace user placeholder if username is provided
		if username != "" {
			scoped = strings.ReplaceAll(scoped, "{user}", username)
		}
		
		result[i] = scoped
	}
	
	return result
}

// IsValidNATSSubject checks if a subject is valid for NATS
func IsValidNATSSubject(subject string) bool {
	if subject == "" {
		return false
	}
	
	// NATS subjects can contain alphanumeric characters, dots, underscores, 
	// hyphens, and wildcards (* and >)
	reg := regexp.MustCompile(`^[a-zA-Z0-9._\-*>]+$`)
	return reg.MatchString(subject)
}

// ValidateSubjects validates an array of NATS subjects
func ValidateSubjects(subjects []string) error {
	for i, subject := range subjects {
		if !IsValidNATSSubject(subject) {
			return fmt.Errorf("invalid NATS subject at index %d: %q", i, subject)
		}
	}
	return nil
}

// PocketBase utility functions
// ===========================

// SetRecordDefaults sets default values for common fields
func SetRecordDefaults(record *core.Record) {
	// Set default timestamps if they don't exist
	now := time.Now()
	
	if record.GetDateTime("created").IsZero() {
		record.Set("created", now)
	}
	
	if record.GetDateTime("updated").IsZero() {
		record.Set("updated", now)
	}
	
	// Set default active status if not set (check if field exists)
	if record.Get("active") == nil {
		record.Set("active", true)
	}
}

// RecordToMap converts a PocketBase record to a map for easier access
func RecordToMap(record *core.Record) map[string]interface{} {
	result := make(map[string]interface{})
	
	// Get all field names from the collection
	for _, field := range record.Collection().Fields {
		fieldName := field.GetName()
		
		// Get the value based on field type
		switch field.Type() {
		case "text", "email", "url":
			result[fieldName] = record.GetString(fieldName)
		case "number":
			result[fieldName] = record.GetFloat(fieldName)
		case "bool":
			result[fieldName] = record.GetBool(fieldName)
		case "date":
			result[fieldName] = record.GetDateTime(fieldName)
		case "json":
			result[fieldName] = record.GetString(fieldName) // JSON as string
		case "relation":
			result[fieldName] = record.GetString(fieldName) // Relation ID
		default:
			result[fieldName] = record.Get(fieldName)
		}
	}
	
	// Always include standard fields using proper methods
	result["id"] = record.Id
	result["created"] = record.GetDateTime("created")
	result["updated"] = record.GetDateTime("updated")
	
	return result
}

// Type conversion utility functions
// ================================

// SafeString safely converts an interface to string
func SafeString(value interface{}) string {
	if value == nil {
		return ""
	}
	
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// SafeInt safely converts an interface to int
func SafeInt(value interface{}) int {
	if value == nil {
		return 0
	}
	
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		// Try to parse as number, return 0 if fails
		if i, err := parseInt(v); err == nil {
			return i
		}
		return 0
	default:
		return 0
	}
}

// parseInt is a simple integer parser (internal helper)
func parseInt(s string) (int, error) {
	result := 0
	negative := false
	
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}
	
	start := 0
	if s[0] == '-' {
		negative = true
		start = 1
	} else if s[0] == '+' {
		start = 1
	}
	
	if start >= len(s) {
		return 0, fmt.Errorf("invalid number format")
	}
	
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("invalid character at position %d", i)
		}
		result = result*10 + int(s[i]-'0')
	}
	
	if negative {
		result = -result
	}
	
	return result, nil
}

// SafeBool safely converts an interface to bool
func SafeBool(value interface{}) bool {
	if value == nil {
		return false
	}
	
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	case int, int64, float64:
		return v != 0
	default:
		return false
	}
}

// Validation utility functions
// ============================

// ValidateRequired checks that required string fields are not empty
func ValidateRequired(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required and cannot be empty", fieldName)
	}
	return nil
}

// ValidatePositiveDuration checks that a duration is positive
func ValidatePositiveDuration(d time.Duration, fieldName string) error {
	if d <= 0 {
		return fmt.Errorf("%s must be positive, got: %v", fieldName, d)
	}
	return nil
}

// ValidateURL performs basic URL validation
func ValidateURL(url, fieldName string) error {
	if url == "" {
		return fmt.Errorf("%s cannot be empty", fieldName)
	}
	
	// Basic check for protocol
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && 
	   !strings.HasPrefix(url, "nats://") && !strings.HasPrefix(url, "tls://") {
		return fmt.Errorf("%s must include a valid protocol (http://, https://, nats://, tls://)", fieldName)
	}
	
	return nil
}

// ValidateNATSSubjectPermissions validates an array of NATS subject permissions
func ValidateNATSSubjectPermissions(permissions []string, fieldName string) error {
	if len(permissions) == 0 {
		return nil // Empty is allowed
	}
	
	for i, perm := range permissions {
		if err := ValidateRequired(perm, fmt.Sprintf("%s[%d]", fieldName, i)); err != nil {
			return err
		}
		
		if !IsValidNATSSubject(perm) {
			return fmt.Errorf("invalid NATS subject pattern in %s[%d]: %q", fieldName, i, perm)
		}
	}
	
	return nil
}

// ValidateStringLength validates that a string is within length limits
func ValidateStringLength(value, fieldName string, minLen, maxLen int) error {
	length := len(value)
	
	if minLen > 0 && length < minLen {
		return fmt.Errorf("%s must be at least %d characters long, got %d", fieldName, minLen, length)
	}
	
	if maxLen > 0 && length > maxLen {
		return fmt.Errorf("%s must be at most %d characters long, got %d", fieldName, maxLen, length)
	}
	
	return nil
}

// ValidateIntRange validates that an integer is within the specified range
func ValidateIntRange(value int64, fieldName string, min, max int64) error {
	if min != 0 && value < min {
		return fmt.Errorf("%s must be at least %d, got %d", fieldName, min, value)
	}
	
	if max != 0 && value > max {
		return fmt.Errorf("%s must be at most %d, got %d", fieldName, max, value)
	}
	
	return nil
}

// Error utility functions  
// =======================

// WrapError wraps an error with additional context, maintaining the original error
func WrapError(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

// WrapErrorf wraps an error with formatted context
func WrapErrorf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	context := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: %w", context, err)
}
