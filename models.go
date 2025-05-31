package pbnats

import (
	"encoding/json"
	"strings"
	"time"
)

// OrganizationRecord represents an organization that maps to a NATS account
type OrganizationRecord struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`               // Display name
	AccountName       string    `json:"account_name"`       // NATS account name (normalized)
	Description       string    `json:"description"`
	PublicKey         string    `json:"public_key"`         // Account public key
	PrivateKey        string    `json:"private_key"`        // Account private key (plaintext)
	Seed              string    `json:"seed"`               // Account seed (plaintext)
	SigningPublicKey  string    `json:"signing_public_key"` // Account signing public key
	SigningPrivateKey string    `json:"signing_private_key"`// Account signing private key (plaintext)
	SigningSeed       string    `json:"signing_seed"`       // Account signing seed (plaintext)
	JWT               string    `json:"jwt"`                // Generated account JWT
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
	OrganizationID   string     `json:"organization_id"`   // FK to organizations
	RoleID           string     `json:"role_id"`           // FK to nats_roles
	JWT              string     `json:"jwt"`               // Generated user JWT
	CredsFile        string     `json:"creds_file"`        // Complete .creds file
	BearerToken      bool       `json:"bearer_token"`      // Bearer token flag
	JWTExpiresAt     *time.Time `json:"jwt_expires_at"`    // Optional expiration
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
	Name              string `json:"name"`               // Always "stone-age.io"
	PublicKey         string `json:"public_key"`         // Operator public key
	PrivateKey        string `json:"private_key"`        // Operator private key (plaintext)
	Seed              string `json:"seed"`               // Operator seed (plaintext)
	SigningPublicKey  string `json:"signing_public_key"` // Operator signing public key
	SigningPrivateKey string `json:"signing_private_key"`// Operator signing private key (plaintext)
	SigningSeed       string `json:"signing_seed"`       // Operator signing seed (plaintext)
	JWT               string `json:"jwt"`                // Generated operator JWT
	Created           time.Time `json:"created"`
	Updated           time.Time `json:"updated"`
}

// PublishQueueRecord represents a queued account publish operation
type PublishQueueRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"` // FK to organizations
	Action         string    `json:"action"`          // "upsert" or "delete"
	Message        string    `json:"message"`         // Error message if failed
	Attempts       int       `json:"attempts"`        // Number of retry attempts
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
}

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

// NormalizeAccountName creates a valid NATS account name from organization name
func (o *OrganizationRecord) NormalizeAccountName() string {
	if o.AccountName != "" {
		return o.AccountName
	}
	
	// Convert to lowercase and replace spaces/special chars with underscores
	name := strings.ToLower(o.Name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	
	// Remove any characters that aren't alphanumeric or underscore
	var result strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			result.WriteRune(char)
		}
	}
	
	return result.String()
}

// ApplyOrganizationScope applies organization scoping to permission patterns
func ApplyOrganizationScope(permissions []string, orgName string) []string {
	result := make([]string, len(permissions))
	for i, perm := range permissions {
		result[i] = strings.ReplaceAll(perm, "{org}", orgName)
	}
	return result
}

// ApplyUserScope applies user scoping to permission patterns
func ApplyUserScope(permissions []string, orgName, username string) []string {
	result := make([]string, len(permissions))
	for i, perm := range permissions {
		result[i] = strings.ReplaceAll(perm, "{org}", orgName)
		result[i] = strings.ReplaceAll(result[i], "{user}", username)
	}
	return result
}
