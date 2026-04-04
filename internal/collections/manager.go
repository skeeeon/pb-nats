// Package collections handles PocketBase collection initialization
package collections

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager handles creation and management of PocketBase collections required for NATS JWT synchronization.
type Manager struct {
	app     *pocketbase.PocketBase
	options pbtypes.Options
}

// NewManager creates a new collection manager with PocketBase integration.
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	return &Manager{
		app:     app,
		options: options,
	}
}

// InitializeCollections creates or updates all required collections in dependency order.
func (cm *Manager) InitializeCollections() error {
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
	if err := cm.ensureUserPermissionFields(); err != nil {
		return fmt.Errorf("failed to ensure user permission fields: %w", err)
	}
	if err := cm.ensureOperatorSigningKeysFields(); err != nil {
		return fmt.Errorf("failed to ensure operator signing keys fields: %w", err)
	}
	if err := cm.ensureAccountSigningKeysFields(); err != nil {
		return fmt.Errorf("failed to ensure account signing keys fields: %w", err)
	}
	if err := cm.createExportsCollection(); err != nil {
		return fmt.Errorf("failed to create exports collection: %w", err)
	}
	if err := cm.createImportsCollection(); err != nil {
		return fmt.Errorf("failed to create imports collection: %w", err)
	}
	if err := cm.createPublishQueueCollection(); err != nil {
		return fmt.Errorf("failed to create publish queue collection: %w", err)
	}
	return nil
}

// createSystemOperatorCollection creates the system operator collection (hidden from normal users).
func (cm *Manager) createSystemOperatorCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	
	// Hidden collection - only system can access
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	collection.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "public_key", Required: true, Max: 200})
	collection.Fields.Add(&core.TextField{Name: "private_key", Required: true, Max: 200})
	collection.Fields.Add(&core.TextField{Name: "seed", Required: true, Max: 200})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys", Required: false, MaxSize: 10000})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys_private", Required: false, MaxSize: 10000, Hidden: true})
	collection.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	collection.Fields.Add(&core.TextField{Name: "system_account_id", Max: 200})
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// createAccountsCollection creates the NATS accounts collection for tenant isolation.
func (cm *Manager) createAccountsCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(cm.options.AccountCollectionName)
	
	// Locked by default - consuming app should set appropriate API rules
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	// Identity fields
	collection.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "description", Max: 500})
	
	// Key fields
	collection.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	collection.Fields.Add(&core.TextField{Name: "private_key", Max: 200, Hidden: true})
	collection.Fields.Add(&core.TextField{Name: "seed", Max: 200, Hidden: true})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys", Required: false, MaxSize: 10000})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys_private", Required: false, MaxSize: 10000, Hidden: true})
	collection.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})

	// Management fields
	collection.Fields.Add(&core.BoolField{Name: "active"})
	collection.Fields.Add(&core.BoolField{Name: "rotate_keys"})
	collection.Fields.Add(&core.BoolField{Name: "add_signing_key"})
	collection.Fields.Add(&core.TextField{Name: "remove_signing_key", Max: 200})
	
	// Account-level limits (-1 = unlimited, 0 = disabled, positive = limit)
	collection.Fields.Add(&core.NumberField{Name: "max_connections", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_subscriptions", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_data", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_payload", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_jetstream_disk_storage", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_jetstream_memory_storage", OnlyInt: true, Min: types.Pointer(-1.0)})
	
	// Timestamps
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// createRolesCollection creates the roles collection for permission templates.
// Includes allow/deny permissions stored as JSON arrays, response permission fields,
// and per-user resource limits.
func (cm *Manager) createRolesCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(cm.options.RoleCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(cm.options.RoleCollectionName)
	
	// Locked by default - consuming app should set appropriate API rules
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	// Identity fields
	collection.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "description", Max: 500})
	collection.Fields.Add(&core.BoolField{Name: "is_default"})
	
	// Allow permission fields (JSON arrays of subjects)
	collection.Fields.Add(&core.JSONField{Name: "publish_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_permissions", Required: false, MaxSize: 5000})
	
	// Deny permission fields (JSON arrays of subjects - takes precedence over allow)
	collection.Fields.Add(&core.JSONField{Name: "publish_deny_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_deny_permissions", Required: false, MaxSize: 5000})
	
	// Response permission fields for request-reply patterns
	// allow_response: When true, enables response permissions for the user
	collection.Fields.Add(&core.BoolField{Name: "allow_response"})
	// allow_response_max: Maximum responses per request (-1 = unlimited, 0 = default/1)
	collection.Fields.Add(&core.NumberField{Name: "allow_response_max", OnlyInt: true, Min: types.Pointer(-1.0)})
	// allow_response_ttl: Response TTL in seconds (0 = no limit/default)
	collection.Fields.Add(&core.NumberField{Name: "allow_response_ttl", OnlyInt: true, Min: types.Pointer(0.0)})
	
	// Per-user limits (-1 = unlimited, 0 = disabled, positive = limit)
	collection.Fields.Add(&core.NumberField{Name: "max_subscriptions", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_data", OnlyInt: true, Min: types.Pointer(-1.0)})
	collection.Fields.Add(&core.NumberField{Name: "max_payload", OnlyInt: true, Min: types.Pointer(-1.0)})

	// Timestamps
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// createUsersCollection creates the NATS users collection with PocketBase authentication.
func (cm *Manager) createUsersCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(cm.options.UserCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewAuthCollection(cm.options.UserCollectionName)
	
	// Locked by default - consuming app should set appropriate API rules
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	// NATS-specific fields
	collection.Fields.Add(&core.TextField{Name: "nats_username", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "description", Max: 500})
	collection.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	collection.Fields.Add(&core.TextField{Name: "private_key", Max: 200, Hidden: true})
	collection.Fields.Add(&core.TextField{Name: "seed", Max: 200, Hidden: true})

	// Save collection first to get ID for relations
	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save user collection: %w", err)
	}

	// Add relation fields
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	rolesCollection, err := cm.app.FindCollectionByNameOrId(cm.options.RoleCollectionName)
	if err != nil {
		return fmt.Errorf("roles collection not found: %w", err)
	}

	collection.Fields.Add(&core.RelationField{
		Name: "account_id", Required: true, MaxSelect: 1,
		CollectionId: accountsCollection.Id, CascadeDelete: false,
	})
	collection.Fields.Add(&core.RelationField{
		Name: "role_id", Required: true, MaxSelect: 1,
		CollectionId: rolesCollection.Id, CascadeDelete: false,
	})
	collection.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	collection.Fields.Add(&core.TextField{Name: "creds_file", Max: 10000})
	collection.Fields.Add(&core.BoolField{Name: "bearer_token"})
	collection.Fields.Add(&core.DateField{Name: "jwt_expires_at"})
	collection.Fields.Add(&core.BoolField{Name: "regenerate"})
	collection.Fields.Add(&core.BoolField{Name: "active"})

	// Per-user permission overrides (JSON arrays of subjects, merged with role permissions)
	collection.Fields.Add(&core.JSONField{Name: "publish_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "publish_deny_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_deny_permissions", Required: false, MaxSize: 5000})

	return cm.app.Save(collection)
}

// createExportsCollection creates the account exports collection for cross-account communication.
func (cm *Manager) createExportsCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(cm.options.ExportCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(cm.options.ExportCollectionName)

	// Locked by default - consuming app should set appropriate API rules
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save exports collection: %w", err)
	}

	// Get accounts collection for relation
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	collection.Fields.Add(&core.RelationField{
		Name: "account_id", Required: true, MaxSelect: 1,
		CollectionId: accountsCollection.Id, CascadeDelete: true,
	})
	collection.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "subject", Required: true, Max: 500})
	collection.Fields.Add(&core.SelectField{
		Name: "type", Required: true, MaxSelect: 1,
		Values: []string{"stream", "service"},
	})
	collection.Fields.Add(&core.BoolField{Name: "token_req"})
	collection.Fields.Add(&core.SelectField{
		Name: "response_type", MaxSelect: 1,
		Values: []string{"Singleton", "Stream", "Chunked"},
	})
	collection.Fields.Add(&core.NumberField{Name: "response_threshold", OnlyInt: true, Min: types.Pointer(0.0)})
	collection.Fields.Add(&core.NumberField{Name: "account_token_position", OnlyInt: true, Min: types.Pointer(0.0)})
	collection.Fields.Add(&core.BoolField{Name: "advertise"})
	collection.Fields.Add(&core.BoolField{Name: "allow_trace"})
	collection.Fields.Add(&core.TextField{Name: "description", Max: 500})
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// createImportsCollection creates the account imports collection for cross-account communication.
func (cm *Manager) createImportsCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(cm.options.ImportCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(cm.options.ImportCollectionName)

	// Locked by default - consuming app should set appropriate API rules
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save imports collection: %w", err)
	}

	// Get accounts collection for relation
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	collection.Fields.Add(&core.RelationField{
		Name: "account_id", Required: true, MaxSelect: 1,
		CollectionId: accountsCollection.Id, CascadeDelete: true,
	})
	collection.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	collection.Fields.Add(&core.TextField{Name: "subject", Required: true, Max: 500})
	collection.Fields.Add(&core.TextField{Name: "account", Required: true, Max: 200})
	collection.Fields.Add(&core.TextField{Name: "token", Max: 10000})
	collection.Fields.Add(&core.TextField{Name: "local_subject", Max: 500})
	collection.Fields.Add(&core.SelectField{
		Name: "type", Required: true, MaxSelect: 1,
		Values: []string{"stream", "service"},
	})
	collection.Fields.Add(&core.BoolField{Name: "share"})
	collection.Fields.Add(&core.BoolField{Name: "allow_trace"})
	collection.Fields.Add(&core.TextField{Name: "description", Max: 500})
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// createPublishQueueCollection creates the publish queue collection for reliable NATS operations.
func (cm *Manager) createPublishQueueCollection() error {
	_, err := cm.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(pbtypes.PublishQueueCollectionName)
	
	// Hidden collection - only system can access
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil

	if err := cm.app.Save(collection); err != nil {
		return fmt.Errorf("failed to save publish queue collection: %w", err)
	}

	// Get accounts collection for relation
	accountsCollection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return fmt.Errorf("accounts collection not found: %w", err)
	}

	collection.Fields.Add(&core.RelationField{
		Name: "account_id", Required: true, MaxSelect: 1,
		CollectionId: accountsCollection.Id, CascadeDelete: true,
	})
	collection.Fields.Add(&core.SelectField{
		Name: "action", Required: true, MaxSelect: 1,
		Values: []string{pbtypes.PublishActionUpsert, pbtypes.PublishActionDelete},
	})
	collection.Fields.Add(&core.TextField{Name: "message", Max: 1000})
	collection.Fields.Add(&core.NumberField{
		Name: "attempts", OnlyInt: true,
		Min: types.Pointer(0.0), Max: types.Pointer(10.0),
	})
	collection.Fields.Add(&core.DateField{Name: "failed_at"})
	collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	collection.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	return cm.app.Save(collection)
}

// ensureUserPermissionFields adds per-user permission JSON fields to existing
// nats_users collections. This is a migration for deployments created before
// per-user permissions were supported.
func (cm *Manager) ensureUserPermissionFields() error {
	collection, err := cm.app.FindCollectionByNameOrId(cm.options.UserCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; createUsersCollection will handle it
	}

	// Check if already migrated by looking for one of the permission fields
	if collection.Fields.GetByName("publish_permissions") != nil {
		return nil
	}

	collection.Fields.Add(&core.JSONField{Name: "publish_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "publish_deny_permissions", Required: false, MaxSize: 5000})
	collection.Fields.Add(&core.JSONField{Name: "subscribe_deny_permissions", Required: false, MaxSize: 5000})

	return cm.app.Save(collection)
}

// ensureOperatorSigningKeysFields migrates the operator collection from scalar signing key
// fields to JSON array fields. For existing deployments with the old schema.
func (cm *Manager) ensureOperatorSigningKeysFields() error {
	collection, err := cm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet
	}

	if collection.Fields.GetByName("signing_keys") != nil {
		return cm.migrateSigningKeyData(pbtypes.SystemOperatorCollectionName)
	}

	collection.Fields.Add(&core.JSONField{Name: "signing_keys", Required: false, MaxSize: 10000})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys_private", Required: false, MaxSize: 10000, Hidden: true})

	if err := cm.app.Save(collection); err != nil {
		return err
	}

	return cm.migrateSigningKeyData(pbtypes.SystemOperatorCollectionName)
}

// ensureAccountSigningKeysFields migrates the accounts collection from scalar signing key
// fields to JSON array fields. Also adds trigger fields for key management.
func (cm *Manager) ensureAccountSigningKeysFields() error {
	collection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet
	}

	if collection.Fields.GetByName("signing_keys") != nil {
		return cm.migrateSigningKeyData(cm.options.AccountCollectionName)
	}

	collection.Fields.Add(&core.JSONField{Name: "signing_keys", Required: false, MaxSize: 10000})
	collection.Fields.Add(&core.JSONField{Name: "signing_keys_private", Required: false, MaxSize: 10000, Hidden: true})
	collection.Fields.Add(&core.BoolField{Name: "add_signing_key"})
	collection.Fields.Add(&core.TextField{Name: "remove_signing_key", Max: 200})

	if err := cm.app.Save(collection); err != nil {
		return err
	}

	return cm.migrateSigningKeyData(cm.options.AccountCollectionName)
}

// migrateSigningKeyData converts existing records from scalar signing key fields to JSON arrays.
func (cm *Manager) migrateSigningKeyData(collectionName string) error {
	records, err := cm.app.FindAllRecords(collectionName)
	if err != nil {
		return nil // No records to migrate
	}

	for _, record := range records {
		// Skip if already has signing_keys_private data
		if val := record.Get("signing_keys_private"); val != nil {
			if bytes, err := json.Marshal(val); err == nil && len(bytes) > 2 {
				continue
			}
		}

		// Read old scalar fields
		pubKey := record.GetString("signing_public_key")
		privKey := record.GetString("signing_private_key")
		seed := record.GetString("signing_seed")

		if pubKey == "" || seed == "" {
			continue
		}

		now := time.Now()
		pub := []pbtypes.SigningKeyPublic{{PublicKey: pubKey, CreatedAt: now}}
		priv := []pbtypes.SigningKeyPrivate{{PublicKey: pubKey, PrivateKey: privKey, Seed: seed, CreatedAt: now}}

		pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(pub, priv)
		if err != nil {
			continue
		}

		record.Set("signing_keys", pubJSON)
		if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, cm.options.EncryptionKey); err != nil {
			continue
		}

		if err := cm.app.Save(record); err != nil {
			return fmt.Errorf("failed to migrate signing keys for record %s: %w", record.Id, err)
		}
	}

	return nil
}
