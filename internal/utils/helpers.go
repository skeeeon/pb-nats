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
// ========================

// Contains checks if a string contains a substring using simple iteration.
// This provides a dependency-free alternative to strings.Contains with
// explicit behavior for edge cases.
//
// EDGE CASE HANDLING:
// - Empty substring always matches (returns true)
// - Empty string only matches empty substring
// - Case-sensitive matching
//
// PARAMETERS:
//   - s: String to search within
//   - substr: Substring to find
//
// RETURNS:
// - true if substr found in s or substr is empty
// - false if substr not found
//
// PERFORMANCE: O(n*m) worst case, suitable for short strings used in pb-nats
func Contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		IndexOf(s, substr) >= 0)
}

// IndexOf finds the first occurrence of substring in string, returning its index.
// Returns -1 if substring not found, similar to JavaScript's indexOf.
//
// SEARCH ALGORITHM:
// Uses simple string scanning with early termination optimizations.
// Suitable for the short strings typically used in NATS operations.
//
// PARAMETERS:
//   - s: String to search within  
//   - substr: Substring to locate
//
// RETURNS:
// - int: Zero-based index of first occurrence
// - -1 if substring not found
// - 0 if substring is empty (matches at start)
//
// USAGE: Primarily used by Contains() and error message parsing
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

// NormalizeIdentifier converts arbitrary strings to valid identifiers.
// Used for creating NATS-compatible names from user input.
//
// NORMALIZATION PROCESS:
// 1. Convert to lowercase for consistency
// 2. Replace spaces and hyphens with underscores
// 3. Remove non-alphanumeric characters (except underscores)
// 4. Collapse multiple consecutive underscores
// 5. Trim underscores from start/end
// 6. Provide fallback for empty results
//
// PARAMETERS:
//   - input: Raw string to normalize
//
// RETURNS:
// - string: Normalized identifier safe for NATS operations
// - "unnamed" if input produces empty result
//
// EXAMPLES:
// - "My Account!" → "my_account"
// - "test--name" → "test_name"
// - "   " → "unnamed"
// - "Acme Corp @#$" → "acme_corp"
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

// TruncateString limits string length for logging and display purposes.
// Prevents log spam from large JWTs and data structures.
//
// TRUNCATION STRATEGY:
// - Return original if within limit
// - Add "..." suffix for truncated strings (when space allows)
// - Handle edge cases where maxLen is very small
//
// PARAMETERS:
//   - s: String to potentially truncate
//   - maxLen: Maximum allowed length
//
// RETURNS:
// - string: Original or truncated string
// - Never exceeds maxLen characters
//
// USAGE: Primarily used in logging operations to prevent JWT strings
// from overwhelming log output while preserving readability.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	
	if maxLen <= 3 {
		return s[:maxLen]
	}
	
	return s[:maxLen-3] + "..."
}

// UniqueStrings removes duplicate entries from string slice while preserving order.
// Used for cleaning up permission arrays and configuration lists.
//
// DEDUPLICATION ALGORITHM:
// - Maintains first occurrence of each unique string
// - Preserves original order
// - Uses map for O(1) duplicate detection
//
// PARAMETERS:
//   - slice: String slice potentially containing duplicates
//
// RETURNS:
// - []string: New slice with duplicates removed
// - Empty slice if input empty or nil
//
// MEMORY EFFICIENCY:
// Pre-allocates result slice with original capacity for performance,
// though final slice may be smaller after deduplication.
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

// FilterEmptyStrings removes empty and whitespace-only strings from slice.
// Used for cleaning configuration arrays before processing.
//
// FILTERING CRITERIA:
// - Removes empty strings ("")
// - Removes whitespace-only strings (spaces, tabs, newlines)
// - Preserves strings with actual content
//
// PARAMETERS:
//   - slice: String slice to filter
//
// RETURNS:
// - []string: New slice with empty strings removed
// - Preserves original order of non-empty strings
//
// USAGE: Often combined with permission processing to ensure
// only valid NATS subject patterns are included.
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
// ======================

// ParseJSONStringArray converts JSON string to string slice with error handling.
// Used for converting database-stored permission arrays to Go data structures.
//
// JSON PARSING STRATEGY:
// - Handle empty input gracefully (return empty slice)
// - Provide descriptive errors for invalid JSON
// - Support standard JSON array format: ["item1", "item2"]
//
// PARAMETERS:
//   - jsonStr: JSON string representation of string array
//
// RETURNS:
// - []string: Parsed string array
// - error: nil on success, descriptive error on parse failure
//
// ERROR HANDLING:
// Truncates input string in error messages to prevent log spam
// from large malformed JSON structures.
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

// StringArrayToJSON converts string slice to JSON representation.
// Used for storing permission arrays in database JSON fields.
//
// JSON ENCODING STRATEGY:
// - Empty slices produce "[]" for consistency
// - Standard JSON array encoding
// - Error handling for encoding failures (rare)
//
// PARAMETERS:
//   - arr: String slice to convert to JSON
//
// RETURNS:
// - string: JSON array representation
// - error: nil on success, error on encoding failure
//
// CONSISTENCY:
// Always returns valid JSON, even for empty arrays, ensuring
// database constraints are satisfied.
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
// ======================

// ApplySubjectScoping applies organization and user scoping to subject patterns.
// Currently unused due to account-based isolation strategy, but preserved
// for potential future use with complex permission scenarios.
//
// SCOPING STRATEGY:
// - Replace {org} placeholder with organization name
// - Replace {user} placeholder with username (if provided)
// - Leave other patterns unchanged
//
// PARAMETERS:
//   - subjects: Subject patterns potentially containing placeholders
//   - orgName: Organization name for {org} replacement
//   - username: Username for {user} replacement (optional)
//
// RETURNS:
// - []string: Subjects with placeholders replaced
//
// DESIGN NOTE:
// This function exists for completeness but is not used in current
// architecture since account boundaries provide sufficient isolation.
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

// IsValidNATSSubject validates subject patterns for NATS compatibility.
// Ensures subjects contain only characters accepted by NATS servers.
//
// NATS SUBJECT RULES:
// - Alphanumeric characters allowed
// - Dots (.) for hierarchy separation
// - Underscores (_) and hyphens (-) allowed
// - Wildcards: * (single token) and > (multiple tokens)
// - Must not be empty
//
// VALIDATION REGEX:
// Uses pre-compiled regex for performance with repeated validation calls.
//
// PARAMETERS:
//   - subject: Subject pattern to validate
//
// RETURNS:
// - bool: true if subject is valid for NATS, false otherwise
//
// USAGE: Called during permission validation to prevent invalid
// subjects from being stored or applied to user JWTs.
func IsValidNATSSubject(subject string) bool {
	if subject == "" {
		return false
	}
	
	// NATS subjects can contain alphanumeric characters, dots, underscores, 
	// hyphens, and wildcards (* and >)
	reg := regexp.MustCompile(`^[a-zA-Z0-9._\-*>]+$`)
	return reg.MatchString(subject)
}

// ValidateSubjects validates an array of NATS subject patterns.
// Ensures all subjects in a permission set are valid for NATS.
//
// VALIDATION APPROACH:
// - Check each subject individually
// - Provide specific error with subject index for debugging
// - Allow empty arrays (indicates no permissions)
//
// PARAMETERS:
//   - subjects: Array of subject patterns to validate
//
// RETURNS:
// - error: nil if all subjects valid, descriptive error for first invalid subject
//
// ERROR REPORTING:
// Includes subject index and content in error message for easier debugging
// of role configuration issues.
func ValidateSubjects(subjects []string) error {
	for i, subject := range subjects {
		if !IsValidNATSSubject(subject) {
			return fmt.Errorf("invalid NATS subject at index %d: %q", i, subject)
		}
	}
	return nil
}

// PocketBase utility functions
// ============================

// SetRecordDefaults applies standard default values to new PocketBase records.
// Ensures consistent record initialization across different collection types.
//
// DEFAULT VALUE STRATEGY:
// - Set created/updated timestamps if missing
// - Set active status to true if field exists and unset
// - Preserve existing values (idempotent)
//
// PARAMETERS:
//   - record: PocketBase record to apply defaults to
//
// SIDE EFFECTS:
// - Modifies record fields directly
// - Only sets values for fields that don't already have values
//
// IDEMPOTENT BEHAVIOR:
// Safe to call multiple times - existing values are preserved.
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

// RecordToMap converts PocketBase record to map for easier programmatic access.
// Used for debugging, logging, and data export scenarios.
//
// FIELD TYPE HANDLING:
// - Text/email/URL fields: Convert to string
// - Number fields: Convert to float64
// - Boolean fields: Convert to bool  
// - Date fields: Convert to time.Time
// - JSON fields: Keep as string representation
// - Relation fields: Convert to related record ID
// - Other types: Use generic interface{} value
//
// STANDARD FIELDS:
// Always includes id, created, updated from record metadata
// regardless of collection schema.
//
// PARAMETERS:
//   - record: PocketBase record to convert
//
// RETURNS:
// - map[string]interface{}: Field name to value mapping
// - Never returns nil (empty map for records with no fields)
//
// USAGE: Primarily for debugging and logging purposes where
// structured field access is more convenient than record methods.
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
// =================================

// SafeString converts interface{} to string with sensible defaults.
// Used for handling dynamic data from PocketBase records and JSON parsing.
//
// CONVERSION RULES:
// - string: Return as-is
// - []byte: Convert to string
// - nil: Return empty string
// - Other types: Use fmt.Sprintf("%v", value)
//
// PARAMETERS:
//   - value: Value to convert to string
//
// RETURNS:
// - string: String representation, never panics
//
// SAFETY: Handles nil values and unexpected types gracefully
// without panicking, making it safe for dynamic data processing.
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

// SafeInt converts interface{} to int with sensible defaults.
// Used for handling numeric data from PocketBase records and configuration.
//
// CONVERSION RULES:
// - int types: Convert to int
// - float64: Truncate to int
// - string: Attempt parsing, return 0 on failure
// - nil or other types: Return 0
//
// PARAMETERS:
//   - value: Value to convert to int
//
// RETURNS:
// - int: Integer representation, 0 for unparseable values
//
// ERROR HANDLING: Never panics - returns 0 for values that
// cannot be reasonably converted to integers.
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

// parseInt provides simple integer parsing without external dependencies.
// Internal helper for SafeInt() that handles basic integer string formats.
//
// PARSING RULES:
// - Supports positive and negative integers
// - Supports optional + prefix
// - Rejects empty strings and non-numeric characters
// - No support for hex, octal, or scientific notation
//
// PARAMETERS:
//   - s: String to parse as integer
//
// RETURNS:
// - int: Parsed integer value
// - error: nil on success, error describing parse failure
//
// IMPLEMENTATION: Simple character-by-character parsing suitable
// for the basic integer strings encountered in pb-nats operations.
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

// SafeBool converts interface{} to bool with intuitive defaults.
// Used for handling boolean configuration values and flags.
//
// CONVERSION RULES:
// - bool: Return as-is
// - string: "true", "1", "yes" → true, others → false
// - numeric: 0 → false, non-zero → true
// - nil: Return false
//
// PARAMETERS:
//   - value: Value to convert to bool
//
// RETURNS:
// - bool: Boolean representation, false for most unparseable values
//
// INTUITIVE BEHAVIOR: Supports common boolean representations
// found in configuration files and user input.
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

// ValidateRequired ensures string fields are not empty after trimming whitespace.
// Primary validation function used throughout pb-nats for required field checking.
//
// VALIDATION LOGIC:
// - Trims whitespace before checking
// - Empty string after trim → validation error
// - Provides consistent error message format
//
// PARAMETERS:
//   - value: String value to validate
//   - fieldName: Human-readable field name for error messages
//
// RETURNS:
// - error: nil if valid, descriptive error if empty
//
// ERROR FORMAT: "{fieldName} is required and cannot be empty"
// This provides consistent error messages across all validation.
func ValidateRequired(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required and cannot be empty", fieldName)
	}
	return nil
}

// ValidatePositiveDuration ensures duration values are positive (greater than zero).
// Used for validating timeout and interval configuration values.
//
// VALIDATION LOGIC:
// - Zero or negative durations → validation error
// - Positive durations → valid
//
// PARAMETERS:
//   - d: Duration value to validate
//   - fieldName: Human-readable field name for error messages
//
// RETURNS:
// - error: nil if positive, descriptive error if zero or negative
//
// USAGE: Applied to all duration configuration fields to prevent
// invalid timeout and interval values.
func ValidatePositiveDuration(d time.Duration, fieldName string) error {
	if d <= 0 {
		return fmt.Errorf("%s must be positive, got: %v", fieldName, d)
	}
	return nil
}

// ValidateURL performs basic URL format validation for NATS server URLs.
// Ensures URLs have required protocol components.
//
// VALIDATION RULES:
// - Must not be empty
// - Must start with recognized protocol (http://, https://, nats://, tls://)
// - Does not validate hostname resolution or connectivity
//
// PARAMETERS:
//   - url: URL string to validate
//   - fieldName: Human-readable field name for error messages
//
// RETURNS:
// - error: nil if valid format, descriptive error if invalid
//
// SCOPE: Basic format validation only - does not check server availability
// or hostname resolution, which are handled by connection manager.
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

// ValidateNATSSubjectPermissions validates array of NATS subject patterns.
// Combines required field validation with NATS subject format checking.
//
// VALIDATION PROCESS:
// 1. Allow empty arrays (no permissions)
// 2. Validate each subject is not empty
// 3. Validate each subject has valid NATS format
//
// PARAMETERS:
//   - permissions: Array of subject patterns to validate
//   - fieldName: Human-readable field name for error messages
//
// RETURNS:
// - error: nil if all valid, descriptive error for first invalid subject
//
// ERROR DETAILS: Includes subject index in error message for
// easier debugging of role configuration issues.
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

// ValidateStringLength ensures strings are within specified length bounds.
// Used for database field validation and input sanitization.
//
// LENGTH VALIDATION:
// - minLen = 0: No minimum length requirement
// - maxLen = 0: No maximum length requirement
// - Length calculated using len() (byte count, not unicode characters)
//
// PARAMETERS:
//   - value: String to validate length of
//   - fieldName: Human-readable field name for error messages
//   - minLen: Minimum required length (0 = no minimum)
//   - maxLen: Maximum allowed length (0 = no maximum)
//
// RETURNS:
// - error: nil if within bounds, descriptive error if out of range
//
// UNICODE CONSIDERATION: Uses byte length, not character count.
// Suitable for database field validation where byte limits apply.
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

// ValidateIntRange ensures integer values are within specified bounds.
// Used for validating numeric configuration parameters.
//
// RANGE VALIDATION:
// - min = 0: No minimum value requirement
// - max = 0: No maximum value requirement
// - Inclusive bounds checking
//
// PARAMETERS:
//   - value: Integer value to validate
//   - fieldName: Human-readable field name for error messages
//   - min: Minimum allowed value (0 = no minimum)
//   - max: Maximum allowed value (0 = no maximum)
//
// RETURNS:
// - error: nil if within range, descriptive error if out of bounds
//
// USAGE: Applied to connection limits, retry counts, and other
// numeric configuration values that have reasonable ranges.
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

// WrapError creates a wrapped error with additional context while preserving the original error.
// Used throughout pb-nats for consistent error handling and context preservation.
//
// ERROR WRAPPING BENEFITS:
// - Preserves original error for debugging
// - Adds contextual information for better error messages
// - Maintains error chain for unwrapping
//
// PARAMETERS:
//   - err: Original error to wrap (nil returns nil)
//   - context: Additional context string to prepend
//
// RETURNS:
// - error: Wrapped error with context, nil if original error nil
//
// ERROR FORMAT: "{context}: {original error message}"
// Compatible with Go 1.13+ error unwrapping.
func WrapError(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

// WrapErrorf creates a wrapped error with formatted context string.
// Combines error wrapping with printf-style string formatting.
//
// FORMATTING SUPPORT:
// - Uses fmt.Sprintf for context string formatting
// - Supports all standard printf format verbs
// - Preserves original error for unwrapping
//
// PARAMETERS:
//   - err: Original error to wrap (nil returns nil)
//   - format: Printf-style format string for context
//   - args: Arguments for format string
//
// RETURNS:
// - error: Wrapped error with formatted context, nil if original error nil
//
// USAGE: Convenient for including variable information in error context:
//   WrapErrorf(err, "failed to process account %s", accountID)
func WrapErrorf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	context := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s: %w", context, err)
}
