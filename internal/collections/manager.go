// Package collections handles PocketBase collection initialization
package collections

import (
	"fmt"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager handles collection initialization and management
type Manager struct {
	app     *pocketbase.PocketBase
	options pbtypes.Options
}

// NewManager creates a new collection manager
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	return &Manager{
		app:     app,
		options: options,
	}
}

// InitializeCollections creates or updates all required collections
func (cm *Manager) InitializeCollections() error {
	// Initialize in dependency order
	if err := cm.createSystemOperatorCollection(); err != nil {
		return fmt.Errorf("failed to create system operator collection: %w", err)
	}
	
	if err := cm.createAccountsCollection(); err != nil {
		return fmt.Errorf("failed to create accounts collection: %w", err)
	}
	
	if err := cm.createRolesCollection(); err != nil {
		return fmt.Errorf("failed to create roles collection: %w", err)
	}
	
	if err := cm.createUsersCollection(); err != nil {
		return fmt.Errorf("failed to create users collection: %w", err)
	}
	
	if err := cm.createPublishQueueCollection(); err != nil {
		return fmt.Errorf("failed to create publish queue collection: %w", err)
	}

	return nil
}

// createSystemOperatorCollection creates the system operator collection (hidden)
func (cm *Manager) createSystemOperatorCollection() error {
	// Check if collection already exists
	_, err := cm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err == nil {
		// Collection already exists
		return nil
	}

	collection := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	
	// Hidden collection - only system can access
	collection.ListRule = nil  // No access
	collection.ViewRule = nil  // No access
	collection.CreateRule = nil // No access
	collection.UpdateRule = nil // No access
	collection.DeleteRule = nil // No access

	// Add fields
	collection.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "public_key",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "private_key",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "seed",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "signing_public_key",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "signing_private_key",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "signing_seed",
		Required: true,
		Max:      200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "jwt",
		Max:  5000,
	})
	
	// Add timestamps
	collection.Fields.Add(&core.AutodateField{
		Name:     "created",
		OnCreate: true,
	})
	collection.Fields.Add(&core.AutodateField{
		Name:     "updated",
		OnCreate: true,
		OnUpdate: true,
	})

	return cm.app.Save(collection)
}

// createAccountsCollection creates the NATS accounts collection
func (cm *Manager) createAccountsCollection() error {
	// Check if collection already exists
	_, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err == nil {
		// Collection already exists
		return nil
	}

	collection := core.NewBaseCollection(cm.options.AccountCollectionName)
	
	// Security rules - authenticated users can list/view active accounts
	// Only authenticated users with proper permissions can create/update/delete
	collection.ListRule = types.Pointer("@request.auth.id != '' && active = true")
	collection.ViewRule = types.Pointer("@request.auth.id != '' && active = true")
	collection.CreateRule = types.Pointer("@request.auth.id != ''")
	collection.UpdateRule = types.Pointer("@request.auth.id != ''")
	collection.DeleteRule = types.Pointer("@request.auth.id != ''")

	// Add fields
	collection.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name: "description",
		Max:  500,
	})
	collection.Fields.Add(&core.TextField{
		Name: "public_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "private_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "seed",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "signing_public_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "signing_private_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "signing_seed",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "jwt",
		Max:  5000,
	})
	collection.Fields.Add(&core.BoolField{
		Name: "active",
	})
	
	// Add timestamps
	collection.Fields.Add(&core.AutodateField{
		Name:     "created",
		OnCreate: true,
	})
	collection.Fields.Add(&core.AutodateField{
		Name:     "updated",
		OnCreate: true,
		OnUpdate: true,
	})

	return cm.app.Save(collection)
}

// createRolesCollection creates the roles collection
func (cm *Manager) createRolesCollection() error {
	// Check if collection already exists
	_, err := cm.app.FindCollectionByNameOrId(cm.options.RoleCollectionName)
	if err == nil {
		// Collection already exists
		return nil
	}

	collection := core.NewBaseCollection(cm.options.RoleCollectionName)
	
	// Security rules - authenticated users can list/view, authenticated users can modify
	collection.ListRule = types.Pointer("@request.auth.id != ''")
	collection.ViewRule = types.Pointer("@request.auth.id != ''")
	collection.CreateRule = types.Pointer("@request.auth.id != ''")
	collection.UpdateRule = types.Pointer("@request.auth.id != ''")
	collection.DeleteRule = types.Pointer("@request.auth.id != ''")

	// Add fields
	collection.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name:     "publish_permissions",
		Required: false,
		Max:      5000, // JSON array as text
	})
	collection.Fields.Add(&core.TextField{
		Name:     "subscribe_permissions",  
		Required: false,
		Max:      5000, // JSON array as text
	})
	collection.Fields.Add(&core.TextField{
		Name: "description",
		Max:  500,
	})
	collection.Fields.Add(&core.BoolField{
		Name: "is_default",
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "max_connections",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "max_data",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "max_payload",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
	})

	return cm.app.Save(collection)
}

// createUsersCollection creates the NATS users collection (auth collection)
func (cm *Manager) createUsersCollection() error {
	// Check if collection already exists
	_, err := cm.app.FindCollectionByNameOrId(cm.options.UserCollectionName)
	if err == nil {
		// Collection already exists
		return nil
	}

	collection := core.NewAuthCollection(cm.options.UserCollectionName)
	
	// Security rules - users can only access their own records
	// Note: In a real app, you'd want more sophisticated role-based access
	collection.ListRule = types.Pointer("@request.auth.id = id")
	collection.ViewRule = types.Pointer("@request.auth.id = id")
	collection.CreateRule = types.Pointer("@request.auth.id != ''") // Authenticated users can create
	collection.UpdateRule = types.Pointer("@request.auth.id = id")
	collection.DeleteRule = types.Pointer("@request.auth.id = id")

	// Add NATS-specific fields
	collection.Fields.Add(&core.TextField{
		Name:     "nats_username",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name: "description",
		Max:  500,
	})
	collection.Fields.Add(&core.TextField{
		Name: "public_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "private_key",
		Max:  200,
	})
	collection.Fields.Add(&core.TextField{
		Name: "seed",
		Max:  200,
	})

	// Save collection first to get ID for relations
	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save user collection: %w", err)
	}

	// Now add relation fields
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	rolesCollection, err := cm.app.FindCollectionByNameOrId(cm.options.RoleCollectionName)
	if err != nil {
		return fmt.Errorf("roles collection not found: %w", err)
	}

	collection.Fields.Add(&core.RelationField{
		Name:          "account_id",
		Required:      true,
		MaxSelect:     1,
		CollectionId:  accountsCollection.Id,
		CascadeDelete: false,
	})
	collection.Fields.Add(&core.RelationField{
		Name:          "role_id",
		Required:      true,
		MaxSelect:     1,
		CollectionId:  rolesCollection.Id,
		CascadeDelete: false,
	})
	collection.Fields.Add(&core.TextField{
		Name: "jwt",
		Max:  5000,
	})
	collection.Fields.Add(&core.TextField{
		Name: "creds_file",
		Max:  10000,
	})
	collection.Fields.Add(&core.BoolField{
		Name: "bearer_token",
	})
	collection.Fields.Add(&core.DateField{
		Name: "jwt_expires_at",
	})
	// Add regenerate field to trigger JWT regeneration
	collection.Fields.Add(&core.BoolField{
		Name: "regenerate",
	})
	collection.Fields.Add(&core.BoolField{
		Name: "active",
	})

	// Save again with the relation fields
	return cm.app.Save(collection)
}

// createPublishQueueCollection creates the publish queue collection for reliable publishing
func (cm *Manager) createPublishQueueCollection() error {
	// Check if collection already exists
	_, err := cm.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err == nil {
		// Collection already exists
		return nil
	}

	collection := core.NewBaseCollection(pbtypes.PublishQueueCollectionName)
	
	// Hidden collection - only system can access
	collection.ListRule = nil  // No access
	collection.ViewRule = nil  // No access
	collection.CreateRule = nil // No access
	collection.UpdateRule = nil // No access
	collection.DeleteRule = nil // No access

	// First save the collection, then add the relation field
	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save publish queue collection: %w", err)
	}

	// Get accounts collection for relation
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	// Add fields
	collection.Fields.Add(&core.RelationField{
		Name:          "account_id",
		Required:      true,
		MaxSelect:     1,
		CollectionId:  accountsCollection.Id,
		CascadeDelete: true,
	})
	collection.Fields.Add(&core.SelectField{
		Name:      "action",
		Required:  true,
		MaxSelect: 1,
		Values:    []string{pbtypes.PublishActionUpsert, pbtypes.PublishActionDelete},
	})
	collection.Fields.Add(&core.TextField{
		Name: "message",
		Max:  1000,
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "attempts",
		OnlyInt: true,
		Min:     types.Pointer(0.0),
		Max:     types.Pointer(10.0), // Max 10 retry attempts
	})
	
	// Add failed_at field for tracking permanently failed records
	collection.Fields.Add(&core.DateField{
		Name: "failed_at",
	})
	
	// Add timestamps
	collection.Fields.Add(&core.AutodateField{
		Name:     "created",
		OnCreate: true,
	})
	collection.Fields.Add(&core.AutodateField{
		Name:     "updated",
		OnCreate: true,
		OnUpdate: true,
	})

	// Save again with all fields
	return cm.app.Save(collection)
}
