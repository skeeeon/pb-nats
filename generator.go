package pbnats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"text/template"

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
	// Get all users
	users, err := g.getAllUsers()
	if err != nil {
		return "", fmt.Errorf("failed to get users: %w", err)
	}

	// Get all roles
	roles, err := g.getAllRoles()
	if err != nil {
		return "", fmt.Errorf("failed to get roles: %w", err)
	}

	// Prepare configuration data
	configData := PrepareConfigData(users, roles, g.defaultPublish, g.defaultSubscribe)

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

// getAllUsers gets all users from the user collection
func (g *Generator) getAllUsers() ([]UserRecord, error) {
	// Get the user collection
	collection, err := g.app.FindCollectionByNameOrId(g.userCollection)
	if err != nil {
		return nil, fmt.Errorf("user collection not found: %w", err)
	}

	// Use filter expression with API directly
	filter := fmt.Sprintf("%s = true", UserFields.Active)
	recordsData, err := g.app.DAL().RecordQuery(collection).AndFilter(filter).All()
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}

	// Convert to UserRecords
	users := make([]UserRecord, 0, len(recordsData))
	for _, record := range recordsData {
		user := UserRecord{
			ID:       record.Id,
			Username: record.GetString(UserFields.Username),
			Password: record.GetString(UserFields.Password),
			RoleID:   record.GetString(UserFields.RoleID),
			Active:   record.GetBool(UserFields.Active),
		}
		users = append(users, user)
	}

	return users, nil
}

// getAllRoles gets all roles from the role collection
func (g *Generator) getAllRoles() (map[string]RoleRecord, error) {
	// Get the role collection
	collection, err := g.app.FindCollectionByNameOrId(g.roleCollection)
	if err != nil {
		return nil, fmt.Errorf("role collection not found: %w", err)
	}

	// Query all roles using direct API
	recordsData, err := g.app.DAL().RecordQuery(collection).All()
	if err != nil {
		return nil, fmt.Errorf("failed to query roles: %w", err)
	}

	// Convert to RoleRecords
	roles := make(map[string]RoleRecord, len(recordsData))
	for _, record := range recordsData {
		// Get raw JSON fields
		publishPerms, err := g.getRawJSONField(record, RoleFields.PublishPermissions)
		if err != nil {
			log.Printf("Warning: Failed to parse publish permissions for role %s: %v", 
				record.Id, err)
		}

		subscribePerms, err := g.getRawJSONField(record, RoleFields.SubscribePermissions)
		if err != nil {
			log.Printf("Warning: Failed to parse subscribe permissions for role %s: %v", 
				record.Id, err)
		}

		role := RoleRecord{
			ID:                   record.Id,
			Name:                 record.GetString(RoleFields.Name),
			PublishPermissions:   publishPerms,
			SubscribePermissions: subscribePerms,
		}
		roles[record.Id] = role
	}

	return roles, nil
}

// getRawJSONField extracts a JSON field from a record
func (g *Generator) getRawJSONField(record *core.Record, fieldName string) (json.RawMessage, error) {
	// Get the field value as a string
	jsonStr := record.GetString(fieldName)
	if jsonStr == "" {
		return json.RawMessage("[]"), nil
	}

	// Validate that it's valid JSON
	var result json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON in field %s: %w", fieldName, err)
	}

	return result, nil
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
