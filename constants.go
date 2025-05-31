// Package pbnats provides seamless integration between PocketBase and NATS server
package pbnats

// This file re-exports constants from internal/types for external use.
// All constant definitions have been moved to internal/types to avoid circular imports.
// They are re-exported in nats.go for external consumption.

// The following constants are available (re-exported in nats.go):

// Collection names:
// - DefaultOrganizationCollectionName = "organizations"
// - DefaultUserCollectionName = "nats_users"  
// - DefaultRoleCollectionName = "nats_roles"
// - SystemOperatorCollectionName = "nats_system_operator"
// - PublishQueueCollectionName = "nats_publish_queue"

// Default operator name:
// - DefaultOperatorName = "stone-age.io"

// Publishing actions:
// - PublishActionUpsert = "upsert"
// - PublishActionDelete = "delete"

// Event types:
// - EventTypeOrgCreate = "org_create"
// - EventTypeOrgUpdate = "org_update"
// - EventTypeOrgDelete = "org_delete"
// - EventTypeUserCreate = "user_create"
// - EventTypeUserUpdate = "user_update"
// - EventTypeUserDelete = "user_delete"
// - EventTypeRoleCreate = "role_create"
// - EventTypeRoleUpdate = "role_update"
// - EventTypeRoleDelete = "role_delete"

// Default permissions:
// - DefaultOrgPublish = "{org}.>"
// - DefaultUserPublish = "{org}.user.{user}.>"
// - DefaultInboxSubscribe = "_INBOX.>"

// Default subscribe permissions (variables):
// - DefaultOrgSubscribe = []string{"{org}.>", "_INBOX.>"}
// - DefaultUserSubscribe = []string{"{org}.>", "_INBOX.>"}

// Access these constants through the main package:
// Example: pbnats.DefaultOrganizationCollectionName
