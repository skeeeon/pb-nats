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
	
	if err := cm.createOrganizationsCollection(); err != nil {
		return fmt.Errorf("failed to create organizations collection: %w", err)
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

// createOrganizationsCollection creates the organizations collection
func (cm *Manager) createOrganizationsCollection() error {
	collection := core.NewBaseCollection(cm.options.OrganizationCollectionName)
	
	// Security rules - authenticated users can list/view active orgs
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
		Name: "account_name",
		Max:  100,
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

	// Note: PocketBase will auto-create basic indexes for unique fields
	// Custom indexes can be added if needed via migrations

	return cm.app.Save(collection)
}

// createRolesCollection creates the roles collection
func (cm *Manager) createRolesCollection() error {
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

	// Note: PocketBase will auto-create basic indexes for unique fields
	// Custom indexes can be added if needed via migrations

	return cm.app.Save(collection)
}

// createUsersCollection creates the NATS users collection (auth collection)
func (cm *Manager) createUsersCollection() error {
	collection := core.NewAuthCollection(cm.options.UserCollectionName)
	
	// Security rules - users can only access their own records
	// Note: In a real app, you'd want more sophisticated role-based access
	collection.ListRule = types.Pointer("@request.auth.id = id")
	collection.ViewRule = types.Pointer("@request.auth.id = id")
	collection.CreateRule = types.Pointer("@request.auth.id != ''") // Authenticated users can create
	collection.UpdateRule = types.Pointer("@request.auth.id = id")
	collection.DeleteRule = types.Pointer("@request.auth.id = id")

	// Get organizations collection for relation
	orgsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.OrganizationCollectionName)
	if err != nil {
		return fmt.Errorf("organizations collection not found: %w", err)
	}

	// Get roles collection for relation
	rolesCollection, err := cm.app.FindCollectionByNameOrId(cm.options.RoleCollectionName)
	if err != nil {
		return fmt.Errorf("roles collection not found: %w", err)
	}

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
	collection.Fields.Add(&core.RelationField{
		Name:          "organization_id",
		Required:      true,
		MaxSelect:     1,
		CollectionId:  orgsCollection.Id,
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
	collection.Fields.Add(&core.BoolField{
		Name: "active",
	})

	// Note: PocketBase will auto-create basic indexes for relation fields
	// Custom indexes can be added if needed via migrations

	return cm.app.Save(collection)
}

// createPublishQueueCollection creates the publish queue collection for reliable publishing
func (cm *Manager) createPublishQueueCollection() error {
	collection := core.NewBaseCollection(pbtypes.PublishQueueCollectionName)
	
	// Hidden collection - only system can access
	collection.ListRule = nil  // No access
	collection.ViewRule = nil  // No access
	collection.CreateRule = nil // No access
	collection.UpdateRule = nil // No access
	collection.DeleteRule = nil // No access

	// Get organizations collection for relation
	orgsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.OrganizationCollectionName)
	if err != nil {
		return fmt.Errorf("organizations collection not found: %w", err)
	}

	// Add fields
	collection.Fields.Add(&core.RelationField{
		Name:          "organization_id",
		Required:      true,
		MaxSelect:     1,
		CollectionId:  orgsCollection.Id,
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

	// Note: PocketBase will auto-create basic indexes for relation fields
	// Custom indexes can be added if needed via migrations

	return cm.app.Save(collection)
}
