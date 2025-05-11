package pbnats

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// NatsConfigData contains the data for the NATS configuration template
type NatsConfigData struct {
	Timestamp        string
	DefaultPublish   string
	DefaultSubscribe string
	Roles            []NatsRole
	Users            []NatsUser
}

// NatsRole represents a role in the NATS configuration
type NatsRole struct {
	Name                 string
	PublishPermissions   string
	SubscribePermissions string
}

// NatsUser represents a user in the NATS configuration
type NatsUser struct {
	Username string
	Password string
	RoleName string
	IsLast   bool
}

// UserRecord represents a user from the MQTT users collection
type UserRecord struct {
	ID       string
	Username string
	Password string
	RoleID   string
	Active   bool
}

// RoleRecord represents a role from the MQTT roles collection
type RoleRecord struct {
	ID                   string
	Name                 string
	PublishPermissions   json.RawMessage
	SubscribePermissions json.RawMessage
}

// FormatDefaultPermissions formats the default permissions for NATS config
func FormatDefaultPermissions(publish, subscribe interface{}) (string, string) {
	// Format publish permission
	var publishStr string
	switch p := publish.(type) {
	case string:
		publishStr = `"` + p + `"`
	case []interface{}:
		var pubs []string
		for _, pub := range p {
			if pubStr, ok := pub.(string); ok {
				pubs = append(pubs, `"`+pubStr+`"`)
			}
		}
		if len(pubs) == 0 {
			publishStr = `""`
		} else if len(pubs) == 1 {
			publishStr = pubs[0]
		} else {
			publishStr = "[" + strings.Join(pubs, ", ") + "]"
		}
	case []string:
		if len(p) == 0 {
			publishStr = `""`
		} else if len(p) == 1 {
			publishStr = `"` + p[0] + `"`
		} else {
			var pubs []string
			for _, pub := range p {
				pubs = append(pubs, `"`+pub+`"`)
			}
			publishStr = "[" + strings.Join(pubs, ", ") + "]"
		}
	default:
		publishStr = `""`
	}

	// Format subscribe permission
	var subscribeStr string
	switch s := subscribe.(type) {
	case string:
		subscribeStr = `"` + s + `"`
	case []interface{}:
		var subs []string
		for _, sub := range s {
			if subStr, ok := sub.(string); ok {
				subs = append(subs, `"`+subStr+`"`)
			}
		}
		if len(subs) == 0 {
			subscribeStr = `""`
		} else if len(subs) == 1 {
			subscribeStr = subs[0]
		} else {
			subscribeStr = "[" + strings.Join(subs, ", ") + "]"
		}
	case []string:
		if len(s) == 0 {
			subscribeStr = `""`
		} else if len(s) == 1 {
			subscribeStr = `"` + s[0] + `"`
		} else {
			var subs []string
			for _, sub := range s {
				subs = append(subs, `"`+sub+`"`)
			}
			subscribeStr = "[" + strings.Join(subs, ", ") + "]"
		}
	default:
		subscribeStr = `""`
	}

	return publishStr, subscribeStr
}

// NormalizeRoleName ensures the role name is valid for NATS config
func (r *RoleRecord) NormalizeRoleName() string {
	// Convert to uppercase and replace spaces/special chars with underscores
	name := strings.ToUpper(r.Name)
	name = strings.ReplaceAll(name, " ", "_")
	
	// Remove any characters that aren't alphanumeric or underscore
	var result strings.Builder
	for _, char := range name {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			result.WriteRune(char)
		}
	}
	
	return result.String()
}

// GetPublishPermissions extracts the string array from the JSON field
func (r *RoleRecord) GetPublishPermissions() ([]string, error) {
	var permissions []string
	if len(r.PublishPermissions) == 0 {
		return permissions, nil
	}
	
	if err := json.Unmarshal(r.PublishPermissions, &permissions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal publish permissions: %w", err)
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
		return nil, fmt.Errorf("failed to unmarshal subscribe permissions: %w", err)
	}
	return permissions, nil
}

// FormatPublishPermissions formats the publish permissions for NATS config
func (r *RoleRecord) FormatPublishPermissions() string {
	permissions, err := r.GetPublishPermissions()
	if err != nil {
		// In case of error, return empty string as default
		return `""`
	}
	
	if len(permissions) == 0 {
		return `""`
	}
	
	if len(permissions) == 1 {
		return `"` + permissions[0] + `"`
	}
	
	var result strings.Builder
	result.WriteString("[")
	for i, perm := range permissions {
		result.WriteString(`"` + perm + `"`)
		if i < len(permissions)-1 {
			result.WriteString(", ")
		}
	}
	result.WriteString("]")
	
	return result.String()
}

// FormatSubscribePermissions formats the subscribe permissions for NATS config
func (r *RoleRecord) FormatSubscribePermissions() string {
	permissions, err := r.GetSubscribePermissions()
	if err != nil {
		// In case of error, return empty string as default
		return `""`
	}
	
	if len(permissions) == 0 {
		return `""`
	}
	
	if len(permissions) == 1 {
		return `"` + permissions[0] + `"`
	}
	
	var result strings.Builder
	result.WriteString("[")
	for i, perm := range permissions {
		result.WriteString(`"` + perm + `"`)
		if i < len(permissions)-1 {
			result.WriteString(", ")
		}
	}
	result.WriteString("]")
	
	return result.String()
}

// PrepareConfigData prepares data for NATS configuration template
func PrepareConfigData(users []UserRecord, roles map[string]RoleRecord, defaultPublish, defaultSubscribe interface{}) *NatsConfigData {
	// Format default permissions
	defaultPublishStr, defaultSubscribeStr := FormatDefaultPermissions(defaultPublish, defaultSubscribe)

	// Create data structure
	configData := &NatsConfigData{
		Timestamp:        time.Now().Format(time.RFC3339),
		DefaultPublish:   defaultPublishStr,
		DefaultSubscribe: defaultSubscribeStr,
		Roles:            make([]NatsRole, 0, len(roles)),
		Users:            make([]NatsUser, 0, len(users)),
	}

	// Add roles
	for _, role := range roles {
		configData.Roles = append(configData.Roles, NatsRole{
			Name:                 role.NormalizeRoleName(),
			PublishPermissions:   role.FormatPublishPermissions(),
			SubscribePermissions: role.FormatSubscribePermissions(),
		})
	}

	// Add users (only active ones with valid roles)
	for _, user := range users {
		// Skip inactive users
		if !user.Active {
			continue
		}

		// Skip users with missing or invalid roles
		role, ok := roles[user.RoleID]
		if !ok {
			continue // Skip users with unknown roles
		}

		configData.Users = append(configData.Users, NatsUser{
			Username: fmt.Sprintf("\"%s\"", user.Username),
			Password: user.Password, // Should already be hashed
			RoleName: role.NormalizeRoleName(),
			IsLast:   false, // Will be set correctly below
		})
	}

	// Sort roles by name for consistent output
	sort.Slice(configData.Roles, func(i, j int) bool {
		return configData.Roles[i].Name < configData.Roles[j].Name
	})

	// Sort users by username
	sort.Slice(configData.Users, func(i, j int) bool {
		return strings.ToLower(configData.Users[i].Username) < strings.ToLower(configData.Users[j].Username)
	})

	// Set IsLast flag for the last user
	if len(configData.Users) > 0 {
		configData.Users[len(configData.Users)-1].IsLast = true
	}

	return configData
}
