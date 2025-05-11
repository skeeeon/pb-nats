package pbnats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"text/template"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// Generator handles generating NATS configuration
type Generator struct {
	app             *pocketbase.PocketBase
	userCollection  string
	roleCollection  string
	defaultPublish  interface{}
	defaultSubscribe interface{}
	logToConsole    bool
}

// NewGenerator creates a new configuration generator
func NewGenerator(app *pocketbase.PocketBase, userCollection, roleCollection string, 
	defaultPublish, defaultSubscribe interface{}, logToConsole bool) *Generator {
	return &Generator{
		app:             app,
		userCollection:  userCollection,
		roleCollection:  roleCollection,
		defaultPublish:  defaultPublish,
		defaultSubscribe: defaultSubscribe,
		logToConsole:    logToConsole,
	}
}

// GenerateConfig generates a NATS configuration file
func (g *Generator) GenerateConfig() (string, error) {
	// Detect if collections exist and handle gracefully
	isFirstRun, err := g.isFirstRun()
	if err != nil {
		return "", fmt.Errorf("failed to check if collections exist: %w", err)
	}

	// First run detection - generate empty but valid config
	if isFirstRun {
		if g.logToConsole {
			log.Printf("First run detected: Either user or role collections don't exist yet, generating minimal config")
		}
		// Create empty data with just default permissions
		configData := PrepareConfigData([]UserRecord{}, map[string]RoleRecord{}, g.defaultPublish, g.defaultSubscribe)
		return g.renderConfig(configData)
	}

	// Get all users
	users, err := g.getAllUsers()
	if err != nil {
		// If we can't get users, don't fail hard - log and continue with empty user set
		if g.logToConsole {
			log.Printf("Warning: Failed to get users: %v", err)
		}
		users = []UserRecord{}
	}

	// Get all roles
	roles, err := g.getAllRoles()
	if err != nil {
		// If we can't get roles, don't fail hard - log and continue with empty role set
		if g.logToConsole {
			log.Printf("Warning: Failed to get roles: %v", err)
		}
		roles = map[string]RoleRecord{}
	}

	// Clean up the data - skip empty roles or users without roles
	cleanedRoles := g.pruneEmptyRoles(roles, users)

	// Prepare configuration data
	configData := PrepareConfigData(users, cleanedRoles, g.defaultPublish, g.defaultSubscribe)

	// Generate configuration
	config, err := g.renderConfig(configData)
	if err != nil {
		return "", fmt.Errorf("failed to render config: %w", err)
	}

	if g.logToConsole {
		log.Printf("Generated NATS configuration with %d roles and %d users", 
			len(configData.Roles), len(configData.Users))
	}

	return config, nil
}

// isFirstRun checks if this is the first run by checking if collections exist
func (g *Generator) isFirstRun() (bool, error) {
	// Check if user collection exists
	_, userErr := g.app.FindCollectionByNameOrId(g.userCollection)
	
	// Check if role collection exists
	_, roleErr := g.app.FindCollectionByNameOrId(g.roleCollection)

	// If either collection doesn't exist, it's a first run
	return (userErr != nil || roleErr != nil), nil
}

// pruneEmptyRoles removes roles that don't have any active users
func (g *Generator) pruneEmptyRoles(roles map[string]RoleRecord, users []UserRecord) map[string]RoleRecord {
	// First, find which roles have active users
	activeRoles := make(map[string]bool)
	for _, user := range users {
		if user.Active {
			activeRoles[user.RoleID] = true
		}
	}

	// Create a new map with only the roles that have active users
	result := make(map[string]RoleRecord)
	for roleID, role := range roles {
		if activeRoles[roleID] {
			result[roleID] = role
		} else if g.logToConsole {
			log.Printf("Skipping role '%s' as it has no active users", role.Name)
		}
	}

	return result
}

// getAllUsers gets all users from the user collection
func (g *Generator) getAllUsers() ([]UserRecord, error) {
	// Get the user collection
	collection, err := g.app.FindCollectionByNameOrId(g.userCollection)
	if err != nil {
		return nil, fmt.Errorf("user collection not found: %w", err)
	}

	// Query active users using direct DB query
	users := []struct {
		ID       string `db:"id"`
		Username string `db:"username"`
		Password string `db:"password"`
		RoleID   string `db:"role_id"`
		Active   bool   `db:"active"`
	}{}

	err = g.app.DB().
		Select("id", UserFields.Username, UserFields.Password, UserFields.RoleID, UserFields.Active).
		From(collection.Name).
		AndWhere(dbx.HashExp{UserFields.Active: true}).
		All(&users)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}

	// Convert to UserRecords
	result := make([]UserRecord, len(users))
	for i, user := range users {
		result[i] = UserRecord{
			ID:       user.ID,
			Username: user.Username,
			Password: user.Password,
			RoleID:   user.RoleID,
			Active:   user.Active,
		}
	}

	return result, nil
}

// getAllRoles gets all roles from the role collection
func (g *Generator) getAllRoles() (map[string]RoleRecord, error) {
	// Get the role collection
	collection, err := g.app.FindCollectionByNameOrId(g.roleCollection)
	if err != nil {
		return nil, fmt.Errorf("role collection not found: %w", err)
	}

	// Query all roles
	roles := []struct {
		ID                   string `db:"id"`
		Name                 string `db:"name"`
		PublishPermissions   string `db:"publish_permissions"`
		SubscribePermissions string `db:"subscribe_permissions"`
	}{}

	err = g.app.DB().
		Select("id", RoleFields.Name, RoleFields.PublishPermissions, RoleFields.SubscribePermissions).
		From(collection.Name).
		All(&roles)
	if err != nil {
		return nil, fmt.Errorf("failed to query roles: %w", err)
	}

	// Convert to RoleRecords
	result := make(map[string]RoleRecord, len(roles))
	for _, role := range roles {
		// Parse JSON fields
		publishPerms, err := g.parseJSONField(role.PublishPermissions)
		if err != nil {
			log.Printf("Warning: Failed to parse publish permissions for role %s: %v", 
				role.ID, err)
			publishPerms = json.RawMessage("[]")
		}

		subscribePerms, err := g.parseJSONField(role.SubscribePermissions)
		if err != nil {
			log.Printf("Warning: Failed to parse subscribe permissions for role %s: %v", 
				role.ID, err)
			subscribePerms = json.RawMessage("[]")
		}

		result[role.ID] = RoleRecord{
			ID:                   role.ID,
			Name:                 role.Name,
			PublishPermissions:   publishPerms,
			SubscribePermissions: subscribePerms,
		}
	}

	return result, nil
}

// parseJSONField parses a JSON string into a json.RawMessage
func (g *Generator) parseJSONField(jsonStr string) (json.RawMessage, error) {
	if jsonStr == "" {
		return json.RawMessage("[]"), nil
	}

	// Validate that it's valid JSON
	var result json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return result, nil
}

// getRawJSONField extracts a JSON field from a record
func (g *Generator) getRawJSONField(record *core.Record, fieldName string) (json.RawMessage, error) {
	// Get the field value as a string
	jsonStr := record.GetString(fieldName)
	return g.parseJSONField(jsonStr)
}

// renderConfig renders the NATS configuration using the template
func (g *Generator) renderConfig(data *NatsConfigData) (string, error) {
	// Parse the template
	tmpl, err := template.New("nats_config").Parse(NatsConfigTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Render the template
	var buffer bytes.Buffer
	err = tmpl.Execute(&buffer, data)
	if err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return buffer.String(), nil
}
