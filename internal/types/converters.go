// Package types provides consolidated record-to-model converters for PocketBase records.
package types

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
)

// RecordToUserModel converts a PocketBase user record to a NatsUserRecord.
func RecordToUserModel(record *core.Record) *NatsUserRecord {
	user := &NatsUserRecord{
		ID:           record.Id,
		NatsUsername: record.GetString("nats_username"),
		Description:  record.GetString("description"),
		PublicKey:    record.GetString("public_key"),
		PrivateKey:   record.GetString("private_key"),
		Seed:         record.GetString("seed"),
		AccountID:    record.GetString("account_id"),
		RoleID:       record.GetString("role_id"),
		JWT:          record.GetString("jwt"),
		CredsFile:    record.GetString("creds_file"),
		BearerToken:  record.GetBool("bearer_token"),
		Active:       record.GetBool("active"),
		Regenerate:   record.GetBool("regenerate"),
	}

	// Marshal per-user permission JSON fields
	marshalJSONField(record, "publish_permissions", &user.PublishPermissions)
	marshalJSONField(record, "subscribe_permissions", &user.SubscribePermissions)
	marshalJSONField(record, "publish_deny_permissions", &user.PublishDenyPermissions)
	marshalJSONField(record, "subscribe_deny_permissions", &user.SubscribeDenyPermissions)

	return user
}

// RecordToAccountModel converts a PocketBase account record to an AccountRecord.
func RecordToAccountModel(record *core.Record) *AccountRecord {
	return &AccountRecord{
		ID:                        record.Id,
		Name:                      record.GetString("name"),
		Description:               record.GetString("description"),
		PublicKey:                 record.GetString("public_key"),
		PrivateKey:                record.GetString("private_key"),
		Seed:                      record.GetString("seed"),
		SigningPublicKey:          record.GetString("signing_public_key"),
		SigningPrivateKey:         record.GetString("signing_private_key"),
		SigningSeed:               record.GetString("signing_seed"),
		JWT:                       record.GetString("jwt"),
		Active:                    record.GetBool("active"),
		MaxConnections:            int64(record.GetInt("max_connections")),
		MaxSubscriptions:          int64(record.GetInt("max_subscriptions")),
		MaxData:                   int64(record.GetInt("max_data")),
		MaxPayload:                int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:   int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage: int64(record.GetInt("max_jetstream_memory_storage")),
	}
}

// RecordToRoleModel converts a PocketBase role record to a RoleRecord.
func RecordToRoleModel(record *core.Record) *RoleRecord {
	role := &RoleRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		Description:      record.GetString("description"),
		IsDefault:        record.GetBool("is_default"),
		AllowResponse:    record.GetBool("allow_response"),
		AllowResponseMax: record.GetInt("allow_response_max"),
		AllowResponseTTL: record.GetInt("allow_response_ttl"),
		MaxSubscriptions: int64(record.GetInt("max_subscriptions")),
		MaxData:          int64(record.GetInt("max_data")),
		MaxPayload:       int64(record.GetInt("max_payload")),
		Created:          record.GetDateTime("created").Time(),
		Updated:          record.GetDateTime("updated").Time(),
	}

	// Marshal JSON fields from record.Get() which returns the actual data structure
	marshalJSONField(record, "publish_permissions", &role.PublishPermissions)
	marshalJSONField(record, "subscribe_permissions", &role.SubscribePermissions)
	marshalJSONField(record, "publish_deny_permissions", &role.PublishDenyPermissions)
	marshalJSONField(record, "subscribe_deny_permissions", &role.SubscribeDenyPermissions)

	return role
}

// marshalJSONField extracts a JSON field from a PocketBase record and marshals it.
func marshalJSONField(record *core.Record, field string, target *json.RawMessage) {
	if val := record.Get(field); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			*target = bytes
		}
	}
}
