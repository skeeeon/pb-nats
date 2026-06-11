package types

import (
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef" // 32 characters

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plaintext := "SUACSEEDVALUE12345"

	encrypted, err := EncryptField(plaintext, testKey)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	if !strings.HasPrefix(encrypted, encryptedPrefix) {
		t.Errorf("encrypted value missing %q prefix: %q", encryptedPrefix, encrypted)
	}
	if strings.Contains(encrypted, plaintext) {
		t.Error("encrypted value contains plaintext")
	}

	decrypted, err := DecryptField(encrypted, testKey)
	if err != nil {
		t.Fatalf("DecryptField: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("round trip = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptFieldPassthrough(t *testing.T) {
	// Empty key disables encryption
	got, err := EncryptField("secret", "")
	if err != nil || got != "secret" {
		t.Errorf("EncryptField with empty key = (%q, %v), want passthrough", got, err)
	}

	// Empty plaintext is not encrypted
	got, err = EncryptField("", testKey)
	if err != nil || got != "" {
		t.Errorf("EncryptField with empty plaintext = (%q, %v), want empty", got, err)
	}
}

func TestDecryptFieldPassthrough(t *testing.T) {
	// Values without the prefix are returned as-is (backward compatibility)
	got, err := DecryptField("plaintext-seed", testKey)
	if err != nil || got != "plaintext-seed" {
		t.Errorf("DecryptField on unprefixed value = (%q, %v), want passthrough", got, err)
	}

	// Empty key returns stored value unchanged
	got, err = DecryptField("enc::whatever", "")
	if err != nil || got != "enc::whatever" {
		t.Errorf("DecryptField with empty key = (%q, %v), want passthrough", got, err)
	}
}

func TestDecryptFieldWrongKey(t *testing.T) {
	encrypted, err := EncryptField("secret", testKey)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}

	wrongKey := "ffffffffffffffffffffffffffffffff"
	if _, err := DecryptField(encrypted, wrongKey); err == nil {
		t.Error("expected error decrypting with wrong key, got nil")
	}
}
