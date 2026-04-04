package types

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

const encryptedPrefix = "enc::"

// EncryptField encrypts a plaintext string value using AES-256-GCM.
// Returns the encrypted value with an "enc::" prefix.
// Passes through empty strings without encrypting.
// If key is empty, returns plaintext unchanged.
func EncryptField(plaintext, key string) (string, error) {
	if key == "" || plaintext == "" {
		return plaintext, nil
	}

	encrypted, err := security.Encrypt([]byte(plaintext), key)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt field: %w", err)
	}

	return encryptedPrefix + encrypted, nil
}

// DecryptField decrypts a stored value if it has the "enc::" prefix.
// Returns plaintext as-is if no prefix (backward compatibility).
// If key is empty, returns the stored value unchanged.
func DecryptField(stored, key string) (string, error) {
	if key == "" || stored == "" || !strings.HasPrefix(stored, encryptedPrefix) {
		return stored, nil
	}

	ciphertext := strings.TrimPrefix(stored, encryptedPrefix)
	decrypted, err := security.Decrypt(ciphertext, key)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt field: %w", err)
	}

	return string(decrypted), nil
}

// EncryptAndSet encrypts a value and sets it on the record.
// If key is empty, sets the plaintext value directly.
func EncryptAndSet(record *core.Record, field, value, key string) error {
	encrypted, err := EncryptField(value, key)
	if err != nil {
		return fmt.Errorf("failed to encrypt %s: %w", field, err)
	}
	record.Set(field, encrypted)
	return nil
}

// EncryptJSONAndSet encrypts a JSON blob as a single string and sets it on the record.
// If key is empty, sets the raw JSON directly.
func EncryptJSONAndSet(record *core.Record, field string, jsonData json.RawMessage, key string) error {
	if key == "" {
		record.Set(field, jsonData)
		return nil
	}

	encrypted, err := EncryptField(string(jsonData), key)
	if err != nil {
		return fmt.Errorf("failed to encrypt %s: %w", field, err)
	}
	record.Set(field, encrypted)
	return nil
}

// decryptString reads a string field from a record and decrypts it if needed.
func decryptString(record *core.Record, field, key string) string {
	val := record.GetString(field)
	if key == "" || val == "" {
		return val
	}
	decrypted, err := DecryptField(val, key)
	if err != nil {
		return val // fall back to raw value on error
	}
	return decrypted
}

// decryptSigningKeys reads and decrypts the signing_keys_private field from a record.
// Returns the decrypted public and private signing key arrays.
func decryptSigningKeys(record *core.Record, key string) ([]SigningKeyPublic, []SigningKeyPrivate) {
	var pub []SigningKeyPublic
	var priv []SigningKeyPrivate

	// Public signing keys are never encrypted
	if val := record.Get("signing_keys"); val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			_ = json.Unmarshal(bytes, &pub)
		}
	}

	// Private signing keys may be encrypted as a single string blob
	if val := record.Get("signing_keys_private"); val != nil {
		switch v := val.(type) {
		case string:
			// Encrypted blob or plain JSON string
			decrypted, err := DecryptField(v, key)
			if err == nil && decrypted != "" {
				_ = json.Unmarshal([]byte(decrypted), &priv)
			}
		default:
			// Unencrypted JSON value (PocketBase returns parsed JSON)
			if bytes, err := json.Marshal(val); err == nil {
				_ = json.Unmarshal(bytes, &priv)
			}
		}
	}

	return pub, priv
}

// getEncryptionKey extracts the encryption key from a variadic parameter.
func getEncryptionKey(keys []string) string {
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}
