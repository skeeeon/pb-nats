package types

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const testEncryptionKey = "0123456789abcdef0123456789abcdef"

// newOperatorCollection builds the operator collection with the two signing key fields
// declared exactly as production declares them — signing_keys_private is a JSON field,
// which is the whole point of the test below.
func newOperatorCollection(t *testing.T, app *pocketbase.PocketBase) *core.Collection {
	t.Helper()

	operator := core.NewBaseCollection(SystemOperatorCollectionName)
	operator.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	operator.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	operator.Fields.Add(&core.TextField{Name: "private_key", Max: 200})
	operator.Fields.Add(&core.TextField{Name: "seed", Max: 200})
	operator.Fields.Add(&core.JSONField{Name: "signing_keys", MaxSize: 10000})
	operator.Fields.Add(&core.JSONField{Name: "signing_keys_private", MaxSize: 10000, Hidden: true})
	operator.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	if err := app.Save(operator); err != nil {
		t.Fatalf("save operator collection: %v", err)
	}
	return operator
}

// TestRecordToOperatorModel_EncryptedSigningKeysRoundTrip pins the record→model boundary
// for signing keys written with at-rest encryption on.
//
// This reproduces a real defect. decryptSigningKeys used to type-switch on `string` to
// spot an encrypted blob, but PocketBase reads a JSON field back as types.JSONRaw and
// never as a Go string — not even for the value EncryptJSONAndSet had just written. The
// encrypted branch was therefore dead, the fallback tried to parse a JSON string as an
// array, and the error was discarded: every encrypted deployment silently loaded *zero*
// signing keys.
//
// Zero signing keys is fatal, not degraded. LatestSigningKey backs both account JWT
// generation and publisher.getSystemOperator, so account saves fail and the publisher
// never leaves bootstrap mode — with nothing in the logs pointing at encryption.
//
// It has to run through a real save and reload. Asserting against an in-memory record,
// or against a hand-built model, sits one layer below the gap and passes regardless.
func TestRecordToOperatorModel_EncryptedSigningKeysRoundTrip(t *testing.T) {
	app := newTestApp(t)
	collection := newOperatorCollection(t, app)

	pub, priv := NewSigningKeyPair("OSIGNINGPUB", "PSIGNINGPRIV", "SOSIGNINGSEED")
	pubJSON, privJSON, err := MarshalSigningKeys([]SigningKeyPublic{pub}, []SigningKeyPrivate{priv})
	if err != nil {
		t.Fatalf("marshal signing keys: %v", err)
	}

	rec := core.NewRecord(collection)
	rec.Set("name", "encrypted-operator")
	rec.Set("public_key", "OIDENTITYPUB")
	rec.Set("signing_keys", pubJSON)
	if err := EncryptJSONAndSet(rec, "signing_keys_private", privJSON, testEncryptionKey); err != nil {
		t.Fatalf("EncryptJSONAndSet: %v", err)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	// Precondition: the field really did read back as something other than a Go
	// string. If PocketBase ever starts returning one, the original type switch would
	// have worked and this test no longer reproduces the defect.
	fresh, err := app.FindRecordById(SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}
	if _, isString := fresh.Get("signing_keys_private").(string); isString {
		t.Fatal("precondition failed: JSON field read back as a Go string")
	}

	operator := RecordToOperatorModel(fresh, testEncryptionKey)

	latest := operator.LatestSigningKey()
	if latest == nil {
		t.Fatal("no signing keys decoded; account JWT generation and the publisher both fail in this state")
	}
	if latest.PublicKey != "OSIGNINGPUB" {
		t.Errorf("signing key public = %q, want %q", latest.PublicKey, "OSIGNINGPUB")
	}
	if latest.Seed != "SOSIGNINGSEED" {
		t.Errorf("signing key seed = %q, want %q", latest.Seed, "SOSIGNINGSEED")
	}
	if latest.PrivateKey != "PSIGNINGPRIV" {
		t.Errorf("signing key private = %q, want %q", latest.PrivateKey, "PSIGNINGPRIV")
	}
	if keys := operator.AllSigningPublicKeys(); len(keys) != 1 || keys[0] != "OSIGNINGPUB" {
		t.Errorf("AllSigningPublicKeys() = %v, want [OSIGNINGPUB]", keys)
	}
}

// TestRecordToOperatorModel_UnencryptedSigningKeysStillWork guards the other half of the
// discriminator. The unencrypted list is stored as a JSON array rather than a JSON
// string, and both shapes have to keep decoding through the same reader — deployments
// turn encryption on later, so a database can hold either.
func TestRecordToOperatorModel_UnencryptedSigningKeysStillWork(t *testing.T) {
	app := newTestApp(t)
	collection := newOperatorCollection(t, app)

	pub, priv := NewSigningKeyPair("OSIGNINGPUB", "PSIGNINGPRIV", "SOSIGNINGSEED")
	pubJSON, privJSON, err := MarshalSigningKeys([]SigningKeyPublic{pub}, []SigningKeyPrivate{priv})
	if err != nil {
		t.Fatalf("marshal signing keys: %v", err)
	}

	rec := core.NewRecord(collection)
	rec.Set("name", "plain-operator")
	rec.Set("public_key", "OIDENTITYPUB")
	rec.Set("signing_keys", pubJSON)
	if err := EncryptJSONAndSet(rec, "signing_keys_private", privJSON, ""); err != nil {
		t.Fatalf("EncryptJSONAndSet (no key): %v", err)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	fresh, err := app.FindRecordById(SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}

	latest := RecordToOperatorModel(fresh).LatestSigningKey()
	if latest == nil {
		t.Fatal("no signing keys decoded from an unencrypted record")
	}
	if latest.Seed != "SOSIGNINGSEED" {
		t.Errorf("signing key seed = %q, want %q", latest.Seed, "SOSIGNINGSEED")
	}
}

// TestRecordToAccountModel_EncryptedSigningKeysRoundTrip covers the same reader on the
// account side. Accounts sign user JWTs with their own signing keys, so the identical
// failure there means no user in that account can be issued credentials.
func TestRecordToAccountModel_EncryptedSigningKeysRoundTrip(t *testing.T) {
	app := newTestApp(t)

	accounts := core.NewBaseCollection("nats_accounts")
	accounts.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	accounts.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	accounts.Fields.Add(&core.TextField{Name: "private_key", Max: 200})
	accounts.Fields.Add(&core.TextField{Name: "seed", Max: 200})
	accounts.Fields.Add(&core.JSONField{Name: "signing_keys", MaxSize: 10000})
	accounts.Fields.Add(&core.JSONField{Name: "signing_keys_private", MaxSize: 10000, Hidden: true})
	accounts.Fields.Add(&core.TextField{Name: "jwt", Max: 50000})
	accounts.Fields.Add(&core.BoolField{Name: "active"})
	if err := app.Save(accounts); err != nil {
		t.Fatalf("save accounts collection: %v", err)
	}

	pub, priv := NewSigningKeyPair("AACCOUNTSIGNINGPUB", "PACCOUNTSIGNINGPRIV", "SAACCOUNTSIGNINGSEED")
	pubJSON, privJSON, err := MarshalSigningKeys([]SigningKeyPublic{pub}, []SigningKeyPrivate{priv})
	if err != nil {
		t.Fatalf("marshal signing keys: %v", err)
	}

	rec := core.NewRecord(accounts)
	rec.Set("name", "tenant-a")
	rec.Set("public_key", "AACCOUNTPUB")
	rec.Set("signing_keys", pubJSON)
	if err := EncryptJSONAndSet(rec, "signing_keys_private", privJSON, testEncryptionKey); err != nil {
		t.Fatalf("EncryptJSONAndSet: %v", err)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save account record: %v", err)
	}

	fresh, err := app.FindRecordById("nats_accounts", rec.Id)
	if err != nil {
		t.Fatalf("reload account record: %v", err)
	}

	latest := RecordToAccountModel(fresh, testEncryptionKey).LatestSigningKey()
	if latest == nil {
		t.Fatal("no account signing keys decoded; no user in this account can be issued a JWT")
	}
	if latest.Seed != "SAACCOUNTSIGNINGSEED" {
		t.Errorf("signing key seed = %q, want %q", latest.Seed, "SAACCOUNTSIGNINGSEED")
	}
}
