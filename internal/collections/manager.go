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
	if err := cm.ensureUserRevokeField(); err != nil {
		return fmt.Errorf("failed to ensure user revoke field: %w", err)
	}
	if err := cm.ensureSystemOperatorFields(); err != nil {
		return fmt.Errorf("failed to ensure system operator fields: %w", err)
	}
	// Must precede every write to the operator record below: a blank required column
	// fails the whole save, and the backfill saves that record.
	if err := cm.ensureOperatorIdentityOptional(); err != nil {
		return fmt.Errorf("failed to relax operator identity fields: %w", err)
	}
	if err := cm.ensureOperatorSigningKeysFields(); err != nil {
		return fmt.Errorf("failed to ensure operator signing keys fields: %w", err)
	}
	// Must follow ensureOperatorSigningKeysFields: that one moves legacy scalar keys
	// into the array, and this one only acts on an operator still left with none.
	if err := cm.ensureOperatorHasSigningKey(); err != nil {
		return fmt.Errorf("failed to backfill operator signing key: %w", err)
	}
	if err := cm.ensureAccountSigningKeysFields(); err != nil {
		return fmt.Errorf("failed to ensure account signing keys fields: %w", err)
	}
	if err := cm.ensureAccountRevocationsField(); err != nil {
		return fmt.Errorf("failed to ensure account revocations field: %w", err)
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
	if err := cm.ensurePublishQueueFields(); err != nil {
		return fmt.Errorf("failed to ensure publish queue fields: %w", err)
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
	// The identity key: not required, so it can be taken offline. Nothing in a running
	// pb-nats reads it — account JWTs and the CLAIMS.DELETE claim are both signed with a
	// signing key. See ensureOperatorIdentityOptional.
	collection.Fields.Add(&core.TextField{Name: "private_key", Required: false, Max: 200})
	collection.Fields.Add(&core.TextField{Name: "seed", Required: false, Max: 200})
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
	// jwt is generous because the account JWT also carries the revocation list.
	collection.Fields.Add(&core.TextField{Name: "jwt", Max: 50000})

	// Revocation list: {"<user-public-key>": <unix-second cutoff>}. Entries are
	// permanent; they invalidate any user JWT for that key issued at or before the
	// cutoff. Public — contains only public keys and timestamps, no secrets.
	collection.Fields.Add(&core.JSONField{Name: "revocations", Required: false, MaxSize: 50000})

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
	// creds_file is intentionally visible and stored unencrypted (even when at-rest
	// encryption is enabled): it is the API-downloadable credential for NATS clients
	// and is the only secret deliberately exposed through the API.
	collection.Fields.Add(&core.TextField{Name: "creds_file", Max: 10000})
	collection.Fields.Add(&core.BoolField{Name: "bearer_token"})
	collection.Fields.Add(&core.DateField{Name: "jwt_expires_at"})
	collection.Fields.Add(&core.BoolField{Name: "regenerate"})
	// revoke: set true to invalidate this user's currently distributed credentials
	// (adds their key to the account revocation list). Consumed on save.
	collection.Fields.Add(&core.BoolField{Name: "revoke"})
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
//
// account_id is intentionally a plain text field, NOT a relation. Queue records must
// outlive their account so that "delete" operations can still be published to NATS
// after the account record is removed (a cascade-deleting relation would destroy the
// pending delete operation together with the account). account_public_key and
// account_name are snapshots taken at enqueue time for the same reason.
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

	collection.Fields.Add(&core.TextField{Name: "account_id", Required: true, Max: 200})
	collection.Fields.Add(&core.TextField{Name: "account_public_key", Max: 200})
	collection.Fields.Add(&core.TextField{Name: "account_name", Max: 200})
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

// ensurePublishQueueFields migrates existing publish queue collections to the
// account-snapshot schema: converts account_id from a cascade-deleting relation
// to a plain text field and adds the account_public_key/account_name fields.
func (cm *Manager) ensurePublishQueueFields() error {
	collection, err := cm.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet
	}

	changed := false

	if _, ok := collection.Fields.GetByName("account_id").(*core.RelationField); ok {
		// PocketBase forbids changing a field's type in place: re-adding with the
		// original field id fails validation ("Field type cannot be changed").
		// Drop the legacy relation field and add a fresh text field of the same
		// name instead. The publish queue is transient and best-effort, so any
		// in-flight rows that don't survive the column swap are an acceptable
		// loss on upgrade.
		collection.Fields.RemoveByName("account_id")
		collection.Fields.Add(&core.TextField{Name: "account_id", Required: true, Max: 200})
		changed = true
	}

	if collection.Fields.GetByName("account_public_key") == nil {
		collection.Fields.Add(&core.TextField{Name: "account_public_key", Max: 200})
		changed = true
	}
	if collection.Fields.GetByName("account_name") == nil {
		collection.Fields.Add(&core.TextField{Name: "account_name", Max: 200})
		changed = true
	}

	if !changed {
		return nil
	}
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

// ensureUserRevokeField adds the revoke trigger field to existing nats_users
// collections. Migration for deployments created before revocation support.
func (cm *Manager) ensureUserRevokeField() error {
	collection, err := cm.app.FindCollectionByNameOrId(cm.options.UserCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; createUsersCollection will handle it
	}

	if collection.Fields.GetByName("revoke") != nil {
		return nil
	}

	collection.Fields.Add(&core.BoolField{Name: "revoke"})
	return cm.app.Save(collection)
}

// ensureAccountRevocationsField adds the revocations field to existing accounts
// collections and widens the jwt field, which now also carries the (potentially
// large) revocation list. Migration for deployments created before revocation support.
func (cm *Manager) ensureAccountRevocationsField() error {
	collection, err := cm.app.FindCollectionByNameOrId(cm.options.AccountCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; createAccountsCollection will handle it
	}

	changed := false

	if collection.Fields.GetByName("revocations") == nil {
		collection.Fields.Add(&core.JSONField{Name: "revocations", Required: false, MaxSize: 50000})
		changed = true
	}

	// Revocations are embedded in the account JWT, so a busy account's JWT can
	// exceed the original 5000-char limit. Widen it so regeneration can't fail.
	if f, ok := collection.Fields.GetByName("jwt").(*core.TextField); ok && f.Max < 50000 {
		f.Max = 50000
		changed = true
	}

	if !changed {
		return nil
	}
	return cm.app.Save(collection)
}

// ensureSystemOperatorFields adds system_account_id to existing operator collections.
// Migration for deployments created before the operator recorded which account is the
// system account.
//
// Without this, an operator collection predating the field can never acquire it:
// createSystemOperatorCollection returns early when the collection exists, so nothing
// adds it, and PocketBase silently discards writes to fields a collection does not
// declare — so initializeSystemComponents' own repair path (resolve the system account
// by name, then save the id onto the operator) reports success while persisting
// nothing, and runs again identically on every boot.
//
// The consequence is remote from the cause and silent. publisher.getSystemUser
// requires operator.SystemAccountID with no name fallback, so it fails, StartBootstrap
// is never called, and no bootstrap retry ticker is ever created: the process stays in
// bootstrap mode for its whole life, logging queued operations every 30s while making
// zero connection attempts. Nothing else looks wrong, because a NATS server whose
// resolver directory is already populated keeps authenticating every client. The
// breakage only surfaces on meeting an empty resolver — a rebuilt server, or a restore
// onto a new host — where ReconcileAccounts is the thing that should repopulate it and
// is precisely what cannot run.
func (cm *Manager) ensureSystemOperatorFields() error {
	collection, err := cm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; createSystemOperatorCollection will handle it
	}

	if collection.Fields.GetByName("system_account_id") != nil {
		return nil
	}

	collection.Fields.Add(&core.TextField{Name: "system_account_id", Max: 200})
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

// ensureOperatorHasSigningKey registers the operator's own identity key as its first
// signing key, on deployments that have no signing keys at all.
//
// Every account JWT is signed with operator.LatestSigningKey() (sync.generateAccountJWT),
// and the publisher refuses to start without one (getSystemOperator). An operator with an
// empty key list therefore fails every account save and never leaves bootstrap mode — but
// only once something tries. A NATS server whose resolver directory is already populated
// keeps authenticating every client in the meantime, so the deployment looks healthy right
// up until the first account edit.
//
// Deployments predating signing keys land in exactly that state. migrateSigningKeyData
// only copies the legacy scalar fields across, and skips when they are empty; nothing
// anywhere generates a signing key for an operator that already exists, because
// createSystemOperatorWithoutJWT (the only producer) returns early for one.
//
// The identity key is reused deliberately rather than generating a fresh signing key.
// An operator's signing key list lives in the operator JWT, which lives in nats.conf and
// is never published over $SYS — so a newly generated key is untrusted until the operator
// JWT is re-exported and every server restarted. Signing accounts with it before that
// happens would reject them all. The identity key is already trusted (it is the operator
// subject), and it already signed every existing account JWT on these deployments, so
// adopting it changes nothing a running server can observe: no re-export, no restart, no
// downtime. This mirrors nsc's own migration for operators that never used signing keys,
// `nsc reissue operator --convert-to-signing-key`.
//
// The cost is that identity and signing key are now the same key, which blocks anything
// that assumes they differ (keeping the identity offline; strict_signing_key_usage).
// Separating them is a deliberate ceremony — add_signing_key, re-export, restart, re-sign
// accounts, remove_signing_key — not something a library upgrade should do behind an
// operator's back. checkOperatorSigningKeySeparation warns about it on every boot.
//
// Fresh installs never reach the loop body: this runs from InitializeCollections, well
// before initializeSystemComponents creates the operator, and that path generates a
// distinct signing key of its own.
func (cm *Manager) ensureOperatorHasSigningKey() error {
	records, err := cm.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; nothing to backfill
	}

	for _, record := range records {
		operator := pbtypes.RecordToOperatorModel(record, cm.options.EncryptionKey)
		if len(operator.SigningKeysPrivate) > 0 {
			continue
		}

		// Without the seed there is no key material to adopt. Leave the record alone
		// rather than writing a signing key entry that cannot sign.
		if operator.Seed == "" || operator.PublicKey == "" {
			continue
		}

		pub, priv := pbtypes.NewSigningKeyPair(operator.PublicKey, operator.PrivateKey, operator.Seed)
		pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
			[]pbtypes.SigningKeyPublic{pub},
			[]pbtypes.SigningKeyPrivate{priv},
		)
		if err != nil {
			return fmt.Errorf("failed to marshal backfilled signing key for operator %s: %w", record.Id, err)
		}

		record.Set("signing_keys", pubJSON)
		if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, cm.options.EncryptionKey); err != nil {
			return fmt.Errorf("failed to encrypt backfilled signing key for operator %s: %w", record.Id, err)
		}
		if err := cm.app.Save(record); err != nil {
			return fmt.Errorf("failed to backfill signing key for operator %s: %w", record.Id, err)
		}
	}

	return nil
}

// ensureOperatorIdentityOptional drops the required flag from nats_system_operator's
// private_key and seed.
//
// Both are the operator's identity key material, and after the CLAIMS.DELETE claim moved
// onto the signing key (publisher.removeAccountJWT) nothing in a running pb-nats reads
// either one. They stay declared, and every deployment still populates them; they are
// simply no longer load-bearing, which is what makes taking the identity key offline
// possible at all.
//
// Required-and-blank is the specific hazard being removed. PocketBase validates the whole
// record on every save, so a blank required column fails the *next* write to the operator
// record for an unrelated reason — and because pb-nats initializes from OnBootstrap, which
// fires for every command, an operator save it cannot complete takes down `migrate` and
// every other subcommand alongside `serve`. There is then no way to reach the code that
// would relax the flag; the column has to be edited out of band. Relaxing it in advance
// is what keeps that door open.
//
// No backfill: relaxing a constraint cannot strand an existing row.
func (cm *Manager) ensureOperatorIdentityOptional() error {
	collection, err := cm.app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil // Collection doesn't exist yet; createSystemOperatorCollection handles it
	}

	changed := false
	for _, name := range []string{"private_key", "seed"} {
		field, ok := collection.Fields.GetByName(name).(*core.TextField)
		if !ok || !field.Required {
			continue
		}
		field.Required = false
		changed = true
	}

	if !changed {
		return nil
	}
	return cm.app.Save(collection)
}
