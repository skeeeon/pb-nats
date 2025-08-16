// Package collections handles PocketBase collection initialization
package collections

import (
	"fmt"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager handles creation and management of PocketBase collections required for NATS JWT synchronization.
// This component ensures all necessary database structures exist before other components attempt to use them.
//
// COLLECTION ARCHITECTURE:
// - nats_system_operator: Single operator record (root of trust)
// - nats_accounts: Multiple tenant accounts (isolation boundaries)  
// - nats_roles: Permission templates (reusable across accounts)
// - nats_users: NATS users with PocketBase authentication
// - nats_publish_queue: Reliable operation queue
//
// INITIALIZATION ORDER:
// Collections must be created in dependency order to support foreign key relationships.
type Manager struct {
	app     *pocketbase.PocketBase // PocketBase instance for database operations
	options pbtypes.Options        // Configuration options including collection names
}

// NewManager creates a new collection manager with PocketBase integration.
//
// PARAMETERS:
//   - app: PocketBase application instance
//   - options: Configuration including custom collection names
//
// RETURNS:
// - Manager instance ready for collection initialization
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	return &Manager{
		app:     app,
		options: options,
	}
}

// InitializeCollections creates or updates all required collections in dependency order.
// This is idempotent - existing collections are left unchanged.
//
// DEPENDENCY ORDER:
// 1. System operator (no dependencies)
// 2. Accounts (no dependencies)  
// 3. Roles (no dependencies)
// 4. Users (depends on accounts and roles)
// 5. Publish queue (depends on accounts)
//
// IDEMPOTENT BEHAVIOR:
// - Checks if collection exists before creating
// - Skips creation if collection already exists
// - Does not modify existing collection schemas
//
// RETURNS:
// - nil on successful initialization
// - error if any collection creation fails
//
// SIDE EFFECTS:
// - Creates database collections and indexes
// - Sets up collection schemas and security rules
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

// createSystemOperatorCollection creates the system operator collection (hidden from normal users).
// This collection stores the root NATS operator credentials and configuration.
//
// SECURITY MODEL:
// - No public access rules (only system can access)
// - Contains root cryptographic keys
// - Single record per deployment
//
// SCHEMA:
// - Identity fields: name, public_key, private_key, seed
// - Signing fields: signing_public_key, signing_private_key, signing_seed  
// - Generated: jwt (operator JWT for NATS server configuration)
// - Metadata: created, updated timestamps
//
// RETURNS:
// - nil if collection created successfully or already exists
// - error if collection creation fails
//
// SIDE EFFECTS:
// - Creates nats_system_operator collection with no access rules
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

// createAccountsCollection creates the NATS accounts collection for tenant isolation.
// Accounts provide natural boundaries for users and permissions without complex subject scoping.
//
// SECURITY MODEL:
// - Authenticated users can list and view active accounts
// - Only authenticated users can create/update/delete accounts
// - Account isolation handled by NATS, not PocketBase rules
//
// SCHEMA:
// - Identity: name, description, public_key, private_key, seed
// - Signing: signing_public_key, signing_private_key, signing_seed
// - Generated: jwt (account JWT for NATS publishing)
// - Management: active (enable/disable), rotate_keys (trigger rotation)
// - Limits: Account-level resource limits (optional, default -1 = unlimited)
// - Metadata: created, updated timestamps
//
// SPECIAL FIELDS:
// - rotate_keys: Boolean trigger for emergency signing key rotation
// - active: Account enable/disable flag
// - Account limits: Control resources across the entire account
//
// RETURNS:
// - nil if collection created successfully or already exists
// - error if collection creation fails
//
// SIDE EFFECTS:
// - Creates accounts collection with authenticated user access
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

	// Add identity fields
	collection.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name: "description",
		Max:  500,
	})
	
	// Add key fields
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
	
	// Add management fields
	collection.Fields.Add(&core.BoolField{
		Name: "active",
	})
	collection.Fields.Add(&core.BoolField{
		Name: "rotate_keys",
	})
	
	// Add account-level limit fields (optional, default -1 = unlimited)
	collection.Fields.Add(&core.NumberField{
		Name:    "max_connections",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "max_subscriptions",
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
	collection.Fields.Add(&core.NumberField{
		Name:    "max_jetstream_disk_storage",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
	})
	collection.Fields.Add(&core.NumberField{
		Name:    "max_jetstream_memory_storage",
		OnlyInt: true,
		Min:     types.Pointer(-1.0), // -1 means unlimited
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

// createRolesCollection creates the roles collection for permission templates.
// Roles define reusable permission sets that can be applied to users across accounts.
//
// SECURITY MODEL:
// - Authenticated users can list and view all roles
// - Only authenticated users can create/update/delete roles
// - Role permissions stored as JSON arrays for flexibility
//
// PERMISSION STORAGE:
// - publish_permissions: JSON array of NATS subjects for publishing
// - subscribe_permissions: JSON array of NATS subjects for subscribing
// - No subject scoping applied - accounts provide isolation
//
// USER LIMIT FIELDS:
// - max_subscriptions: Maximum concurrent subscriptions per user (-1 = unlimited)
// - max_data: Maximum bytes in flight per user (-1 = unlimited)
// - max_payload: Maximum message size per user (-1 = unlimited)
//
// SCHEMA:
// - Identity: name, description, is_default
// - Permissions: publish_permissions, subscribe_permissions (JSON arrays)
// - Limits: max_subscriptions, max_data, max_payload (per-user limits)
//
// RETURNS:
// - nil if collection created successfully or already exists
// - error if collection creation fails
//
// SIDE EFFECTS:
// - Creates roles collection with authenticated user access
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

	// Add identity fields
	collection.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
		Max:      100,
	})
	collection.Fields.Add(&core.TextField{
		Name: "description",
		Max:  500,
	})
	collection.Fields.Add(&core.BoolField{
		Name: "is_default",
	})
	
	// Add permission fields
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
	
	// Add per-user limit fields
	collection.Fields.Add(&core.NumberField{
		Name:    "max_subscriptions",
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

// createUsersCollection creates the NATS users collection with PocketBase authentication.
// This is an auth collection that extends PocketBase users with NATS-specific fields.
//
// AUTH COLLECTION FEATURES:
// - Built-in email/password authentication
// - Email verification support
// - Standard PocketBase user management
// - Extended with NATS credentials
//
// SECURITY MODEL:
// - Users can only access their own records (self-service)
// - Authenticated users can create new users
// - Admin users can manage all users
//
// NATS INTEGRATION:
// - nats_username: NATS identity (separate from email)
// - Generated keys: public_key, private_key, seed
// - Relations: account_id, role_id (foreign keys)
// - Generated: jwt, creds_file (for NATS connections)
// - Management: regenerate (trigger JWT refresh), active (enable/disable)
//
// SPECIAL FIELDS:
// - regenerate: Boolean trigger for JWT regeneration
// - bearer_token: NATS bearer token authentication flag
// - jwt_expires_at: Optional JWT expiration timestamp
//
// TWO-PHASE CREATION:
// Collection must be saved before adding relation fields due to PocketBase requirements.
//
// RETURNS:
// - nil if collection created successfully or already exists
// - error if collection creation fails
//
// SIDE EFFECTS:
// - Creates auth collection with user self-access
// - Sets up foreign key relationships to accounts and roles
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

// createPublishQueueCollection creates the publish queue collection for reliable NATS operations.
// This collection provides operation persistence during NATS outages and retry logic.
//
// QUEUE RELIABILITY:
// - Operations survive NATS server restarts
// - Automatic retry with attempt counting
// - Failed operation tracking and cleanup
// - Bootstrap mode integration
//
// SECURITY MODEL:
// - Hidden collection (only system access)
// - No public API access
// - Internal operations only
//
// QUEUE RECORD LIFECYCLE:
// 1. Created: attempts=0, failed_at=null
// 2. Processing: attempts incremented on failures
// 3. Success: record deleted
// 4. Max attempts: failed_at set (permanent failure)
// 5. Cleanup: old failed records deleted
//
// SCHEMA:
// - Operation: account_id (FK), action (upsert/delete)
// - Status: attempts, message (error details), failed_at
// - Metadata: created, updated timestamps
//
// TWO-PHASE CREATION:
// Collection saved first, then relation added due to PocketBase requirements.
//
// RETURNS:
// - nil if collection created successfully or already exists  
// - error if collection creation fails
//
// SIDE EFFECTS:
// - Creates hidden queue collection
// - Sets up foreign key relationship to accounts
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
