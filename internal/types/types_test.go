package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "myaccount", "myaccount"},
		{"spaces", "My Account", "my_account"},
		{"hyphens", "test-name", "test_name"},
		{"special chars", "Acme Corp @#$", "acme_corp_"},
		{"empty", "", "unnamed_account"},
		{"only special chars", "@#$%", "unnamed_account"},
		{"system account", "System Account", "system_account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AccountRecord{Name: tt.in}
			if got := a.NormalizeName(); got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseJSONPermissions(t *testing.T) {
	role := &RoleRecord{
		PublishPermissions:   json.RawMessage(`["events.>", "commands.*"]`),
		SubscribePermissions: nil,
	}

	pub, err := role.GetPublishPermissions()
	if err != nil {
		t.Fatalf("GetPublishPermissions: %v", err)
	}
	if len(pub) != 2 || pub[0] != "events.>" || pub[1] != "commands.*" {
		t.Errorf("GetPublishPermissions = %v, want [events.> commands.*]", pub)
	}

	sub, err := role.GetSubscribePermissions()
	if err != nil {
		t.Fatalf("GetSubscribePermissions: %v", err)
	}
	if len(sub) != 0 {
		t.Errorf("GetSubscribePermissions on nil field = %v, want empty", sub)
	}

	bad := &RoleRecord{PublishPermissions: json.RawMessage(`{"not": "an array"}`)}
	if _, err := bad.GetPublishPermissions(); err == nil {
		t.Error("expected error for non-array JSON, got nil")
	}
}

func TestLatestSigningKey(t *testing.T) {
	a := &AccountRecord{}
	if a.LatestSigningKey() != nil {
		t.Error("expected nil for account with no signing keys")
	}

	a.SigningKeysPrivate = []SigningKeyPrivate{
		{PublicKey: "first", Seed: "seed1"},
		{PublicKey: "second", Seed: "seed2"},
	}
	latest := a.LatestSigningKey()
	if latest == nil || latest.PublicKey != "second" {
		t.Errorf("LatestSigningKey = %+v, want the last key (second)", latest)
	}

	o := &SystemOperatorRecord{}
	if o.LatestSigningKey() != nil {
		t.Error("expected nil for operator with no signing keys")
	}
}

func TestMarshalSigningKeysRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	pub, priv := NewSigningKeyPair("PUBKEY", "PRIVKEY", "SEED")
	if pub.PublicKey != "PUBKEY" || priv.PublicKey != "PUBKEY" || priv.Seed != "SEED" {
		t.Fatalf("NewSigningKeyPair produced mismatched pair: %+v / %+v", pub, priv)
	}
	if pub.CreatedAt.Before(now.Add(-time.Minute)) {
		t.Error("CreatedAt not set to current time")
	}

	pubJSON, privJSON, err := MarshalSigningKeys([]SigningKeyPublic{pub}, []SigningKeyPrivate{priv})
	if err != nil {
		t.Fatalf("MarshalSigningKeys: %v", err)
	}

	var gotPub []SigningKeyPublic
	var gotPriv []SigningKeyPrivate
	if err := json.Unmarshal(pubJSON, &gotPub); err != nil {
		t.Fatalf("unmarshal public keys: %v", err)
	}
	if err := json.Unmarshal(privJSON, &gotPriv); err != nil {
		t.Fatalf("unmarshal private keys: %v", err)
	}
	if len(gotPub) != 1 || gotPub[0].PublicKey != "PUBKEY" {
		t.Errorf("public round-trip = %+v", gotPub)
	}
	if len(gotPriv) != 1 || gotPriv[0].Seed != "SEED" {
		t.Errorf("private round-trip = %+v", gotPriv)
	}
}

func TestParseRevocations(t *testing.T) {
	// Empty/nil input yields a usable empty map, not nil.
	for _, in := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("{}")} {
		m, err := ParseRevocations(in)
		if err != nil {
			t.Fatalf("ParseRevocations(%q): %v", in, err)
		}
		if m == nil {
			t.Errorf("ParseRevocations(%q) = nil map, want empty non-nil", in)
		}
	}

	m, err := ParseRevocations(json.RawMessage(`{"UABC":100,"UDEF":200}`))
	if err != nil {
		t.Fatalf("ParseRevocations: %v", err)
	}
	if m["UABC"] != 100 || m["UDEF"] != 200 {
		t.Errorf("ParseRevocations = %v, want UABC=100 UDEF=200", m)
	}

	if _, err := ParseRevocations(json.RawMessage(`["not","an","object"]`)); err == nil {
		t.Error("expected error for non-object JSON, got nil")
	}
}

func TestRevokeInJSON(t *testing.T) {
	// Add to empty.
	data, err := RevokeInJSON(nil, "UABC", 100)
	if err != nil {
		t.Fatalf("RevokeInJSON: %v", err)
	}
	m, _ := ParseRevocations(data)
	if m["UABC"] != 100 {
		t.Errorf("after first revoke = %v, want UABC=100", m)
	}

	// A later cutoff advances the entry.
	data, err = RevokeInJSON(data, "UABC", 150)
	if err != nil {
		t.Fatalf("RevokeInJSON: %v", err)
	}
	m, _ = ParseRevocations(data)
	if m["UABC"] != 150 {
		t.Errorf("after advancing = %v, want UABC=150", m)
	}

	// An earlier cutoff is ignored (cutoffs only move forward).
	data, err = RevokeInJSON(data, "UABC", 120)
	if err != nil {
		t.Fatalf("RevokeInJSON: %v", err)
	}
	m, _ = ParseRevocations(data)
	if m["UABC"] != 150 {
		t.Errorf("after earlier cutoff = %v, want UABC unchanged at 150", m)
	}

	// A second key is independent.
	data, err = RevokeInJSON(data, "UDEF", 300)
	if err != nil {
		t.Fatalf("RevokeInJSON: %v", err)
	}
	m, _ = ParseRevocations(data)
	if m["UABC"] != 150 || m["UDEF"] != 300 {
		t.Errorf("final = %v, want UABC=150 UDEF=300", m)
	}
}

func TestGetRevocations(t *testing.T) {
	a := &AccountRecord{Revocations: json.RawMessage(`{"UABC":100}`)}
	m, err := a.GetRevocations()
	if err != nil {
		t.Fatalf("GetRevocations: %v", err)
	}
	if m["UABC"] != 100 {
		t.Errorf("GetRevocations = %v, want UABC=100", m)
	}

	// Account with no revocations returns an empty map.
	empty, err := (&AccountRecord{}).GetRevocations()
	if err != nil {
		t.Fatalf("GetRevocations (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("GetRevocations (empty) = %v, want empty", empty)
	}
}

func TestAllSigningPublicKeys(t *testing.T) {
	a := &AccountRecord{
		SigningKeys: []SigningKeyPublic{{PublicKey: "A"}, {PublicKey: "B"}},
	}
	keys := a.AllSigningPublicKeys()
	if len(keys) != 2 || keys[0] != "A" || keys[1] != "B" {
		t.Errorf("AllSigningPublicKeys = %v, want [A B]", keys)
	}
}
