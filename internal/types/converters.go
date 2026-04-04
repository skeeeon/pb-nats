// Package types provides consolidated record-to-model converters for PocketBase records.
package types

import (
	"encoding/json"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// RecordToUserModel converts a PocketBase user record to a NatsUserRecord.
// Optional encryptionKey decrypts sensitive fields (private_key, seed).
func RecordToUserModel(record *core.Record, encryptionKey ...string) *NatsUserRecord {
	key := getEncryptionKey(encryptionKey)
	user := &NatsUserRecord{
		ID:           record.Id,
		NatsUsername: record.GetString("nats_username"),
		Description:  record.GetString("description"),
		PublicKey:    record.GetString("public_key"),
		PrivateKey:   decryptString(record, "private_key", key),
		Seed:         decryptString(record, "seed", key),
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
// Optional encryptionKey decrypts sensitive fields (private_key, seed, signing_keys_private).
func RecordToAccountModel(record *core.Record, encryptionKey ...string) *AccountRecord {
	key := getEncryptionKey(encryptionKey)
	account := &AccountRecord{
		ID:                        record.Id,
		Name:                      record.GetString("name"),
		Description:               record.GetString("description"),
		PublicKey:                 record.GetString("public_key"),
		PrivateKey:                decryptString(record, "private_key", key),
		Seed:                      decryptString(record, "seed", key),
		JWT:                       record.GetString("jwt"),
		Active:                    record.GetBool("active"),
		MaxConnections:            int64(record.GetInt("max_connections")),
		MaxSubscriptions:          int64(record.GetInt("max_subscriptions")),
		MaxData:                   int64(record.GetInt("max_data")),
		MaxPayload:                int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:   int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage: int64(record.GetInt("max_jetstream_memory_storage")),
	}

	// Parse signing keys from JSON fields (with decryption)
	account.SigningKeys, account.SigningKeysPrivate = decryptSigningKeys(record, key)

	// Fallback: if no signing_keys_private, try old scalar fields (pre-migration)
	if len(account.SigningKeysPrivate) == 0 {
		if pub, priv := signingKeyFromScalarFields(record); priv != nil {
			account.SigningKeys = []SigningKeyPublic{*pub}
			account.SigningKeysPrivate = []SigningKeyPrivate{*priv}
		}
	}

	return account
}

// RecordToOperatorModel converts a PocketBase operator record to a SystemOperatorRecord.
// Optional encryptionKey decrypts sensitive fields (private_key, seed, signing_keys_private).
func RecordToOperatorModel(record *core.Record, encryptionKey ...string) *SystemOperatorRecord {
	key := getEncryptionKey(encryptionKey)
	operator := &SystemOperatorRecord{
		ID:              record.Id,
		Name:            record.GetString("name"),
		PublicKey:       record.GetString("public_key"),
		PrivateKey:      decryptString(record, "private_key", key),
		Seed:            decryptString(record, "seed", key),
		JWT:             record.GetString("jwt"),
		SystemAccountID: record.GetString("system_account_id"),
		Created:         record.GetDateTime("created").Time(),
		Updated:         record.GetDateTime("updated").Time(),
	}

	// Parse signing keys from JSON fields (with decryption)
	operator.SigningKeys, operator.SigningKeysPrivate = decryptSigningKeys(record, key)

	// Fallback: if no signing_keys_private, try old scalar fields (pre-migration)
	if len(operator.SigningKeysPrivate) == 0 {
		if pub, priv := signingKeyFromScalarFields(record); priv != nil {
			operator.SigningKeys = []SigningKeyPublic{*pub}
			operator.SigningKeysPrivate = []SigningKeyPrivate{*priv}
		}
	}

	return operator
}

// RecordToRoleModel converts a PocketBase role record to a RoleRecord.
// Roles contain no sensitive fields, so no encryption handling is needed.
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

// signingKeyFromScalarFields builds signing key entries from old scalar fields (pre-migration fallback).
func signingKeyFromScalarFields(record *core.Record) (*SigningKeyPublic, *SigningKeyPrivate) {
	pubKey := record.GetString("signing_public_key")
	privKey := record.GetString("signing_private_key")
	seed := record.GetString("signing_seed")

	if pubKey == "" || seed == "" {
		return nil, nil
	}

	now := time.Now()
	return &SigningKeyPublic{
			PublicKey: pubKey,
			CreatedAt: now,
		}, &SigningKeyPrivate{
			PublicKey:  pubKey,
			PrivateKey: privKey,
			Seed:      seed,
			CreatedAt: now,
		}
}

// marshalJSONField extracts a JSON field from a PocketBase record and marshals it.
func marshalJSONField(record *core.Record, field string, target *json.RawMessage) {
	if val := record.Get(field); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			*target = bytes
		}
	}
}
