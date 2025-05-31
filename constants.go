// Package pbnats provides seamless integration between PocketBase and NATS server
// by automatically generating and managing NATS JWT authentication.
package pbnats

// Collection names
const (
	DefaultOrganizationCollectionName = "organizations"
	DefaultUserCollectionName         = "nats_users"
	DefaultRoleCollectionName         = "nats_roles"
	SystemOperatorCollectionName      = "nats_system_operator"
	PublishQueueCollectionName        = "nats_publish_queue"
)

// Default operator name
const (
	DefaultOperatorName = "stone-age.io"
)

// Publishing actions for the queue
const (
	PublishActionUpsert = "upsert"
	PublishActionDelete = "delete"
)

// Event types for logging and filtering
const (
	EventTypeOrgCreate    = "org_create"
	EventTypeOrgUpdate    = "org_update"
	EventTypeOrgDelete    = "org_delete"
	EventTypeUserCreate   = "user_create"
	EventTypeUserUpdate   = "user_update"
	EventTypeUserDelete   = "user_delete"
	EventTypeRoleCreate   = "role_create"
	EventTypeRoleUpdate   = "role_update"
	EventTypeRoleDelete   = "role_delete"
)

// Default permissions
const (
	DefaultOrgPublish      = "{org}.>"
	DefaultUserPublish     = "{org}.user.{user}.>"
	DefaultInboxSubscribe  = "_INBOX.>"
)

// Default subscribe permissions
var DefaultOrgSubscribe = []string{"{org}.>", "_INBOX.>"}
var DefaultUserSubscribe = []string{"{org}.>", "_INBOX.>"}
