package collections

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// testOptions supplies the collection names InitializeCollections needs; a zero Options
// fails on the first collection with "name: cannot be blank".
func testOptions() pbtypes.Options {
	return pbtypes.Options{
		AccountCollectionName: pbtypes.DefaultAccountCollectionName,
		UserCollectionName:    pbtypes.DefaultUserCollectionName,
		RoleCollectionName:    pbtypes.DefaultRoleCollectionName,
		ExportCollectionName:  pbtypes.DefaultExportCollectionName,
		ImportCollectionName:  pbtypes.DefaultImportCollectionName,
	}
}

// operatorCollectionWithoutSigningKeys builds the operator collection as it existed
// before signing keys, so the migrations below run against the shape they actually have
// to repair rather than a modern collection with an empty array.
func operatorCollectionWithoutSigningKeys(t *testing.T, app *pocketbase.PocketBase) *core.Collection {
	t.Helper()

	operator := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	operator.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	operator.Fields.Add(&core.TextField{Name: "public_key", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "private_key", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "seed", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	if err := app.Save(operator); err != nil {
		t.Fatalf("save pre-signing-key operator collection: %v", err)
	}
	return operator
}

// TestEnsureOperatorHasSigningKey_AdoptsIdentityKey reproduces a deployment created
// before signing keys existed and asserts the backfill adopts the operator's identity
// key as signing key #1.
//
// The assertion that matters is the *value*: the adopted key must be the identity key,
// not a freshly generated one. A new key is absent from the operator JWT that every
// running nats-server was configured with, so signing accounts with it would reject them
// all until an operator re-exported the config and restarted the fleet. Asserting merely
// that "a signing key now exists" would pass against exactly that outage.
func TestEnsureOperatorHasSigningKey_AdoptsIdentityKey(t *testing.T) {
	app := newTestApp(t)
	operator := operatorCollectionWithoutSigningKeys(t, app)

	rec := core.NewRecord(operator)
	rec.Set("name", "stone-age.io")
	rec.Set("public_key", "OIDENTITYPUBLICKEY")
	rec.Set("private_key", "PIDENTITYPRIVATEKEY")
	rec.Set("seed", "SOIDENTITYSEED")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	// Precondition: no signing key, which is what breaks every account save.
	if before := pbtypes.RecordToOperatorModel(rec); before.LatestSigningKey() != nil {
		t.Fatal("precondition failed: operator already has a signing key")
	}

	cm := NewManager(app, testOptions())
	if err := cm.InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}

	fresh, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}
	got := pbtypes.RecordToOperatorModel(fresh)

	latest := got.LatestSigningKey()
	if latest == nil {
		t.Fatal("operator still has no signing key after backfill")
	}
	if latest.PublicKey != "OIDENTITYPUBLICKEY" {
		t.Errorf("signing key public = %q, want the identity key %q", latest.PublicKey, "OIDENTITYPUBLICKEY")
	}
	if latest.Seed != "SOIDENTITYSEED" {
		t.Errorf("signing key seed = %q, want the identity seed %q", latest.Seed, "SOIDENTITYSEED")
	}
	if pubs := got.AllSigningPublicKeys(); len(pubs) != 1 || pubs[0] != "OIDENTITYPUBLICKEY" {
		t.Errorf("AllSigningPublicKeys() = %v, want [OIDENTITYPUBLICKEY]", pubs)
	}

	// Idempotent: a second pass must not append a duplicate.
	if err := cm.ensureOperatorHasSigningKey(); err != nil {
		t.Fatalf("ensureOperatorHasSigningKey (second run): %v", err)
	}
	again, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record after second run: %v", err)
	}
	if keys := pbtypes.RecordToOperatorModel(again).AllSigningPublicKeys(); len(keys) != 1 {
		t.Errorf("second run changed the key list: %v", keys)
	}
}

// TestEnsureOperatorHasSigningKey_EncryptedRoundTrip runs the backfill with at-rest
// encryption on. The adopted seed is written through EncryptJSONAndSet, so a mismatch
// between how it is stored and how RecordToOperatorModel reads it back would hand the
// generator an unusable key — and the failure would only surface at the first account
// save, far from here.
func TestEnsureOperatorHasSigningKey_EncryptedRoundTrip(t *testing.T) {
	app := newTestApp(t)
	operator := operatorCollectionWithoutSigningKeys(t, app)

	const key = "0123456789abcdef0123456789abcdef"

	rec := core.NewRecord(operator)
	rec.Set("name", "stone-age.io")
	rec.Set("public_key", "OIDENTITYPUBLICKEY")
	if err := pbtypes.EncryptAndSet(rec, "private_key", "PIDENTITYPRIVATEKEY", key); err != nil {
		t.Fatalf("encrypt private_key: %v", err)
	}
	if err := pbtypes.EncryptAndSet(rec, "seed", "SOIDENTITYSEED", key); err != nil {
		t.Fatalf("encrypt seed: %v", err)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	opts := testOptions()
	opts.EncryptionKey = key
	cm := NewManager(app, opts)
	if err := cm.InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}

	fresh, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}

	latest := pbtypes.RecordToOperatorModel(fresh, key).LatestSigningKey()
	if latest == nil {
		t.Fatal("operator still has no signing key after backfill")
	}
	if latest.Seed != "SOIDENTITYSEED" {
		t.Errorf("decrypted signing key seed = %q, want %q", latest.Seed, "SOIDENTITYSEED")
	}

	// The stored value must actually be ciphertext, or "encryption enabled" is a lie.
	if raw := fresh.GetString("signing_keys_private"); raw != "" && raw == string(mustMarshalPlain(t, latest)) {
		t.Error("signing_keys_private was stored in plaintext")
	}
}

func mustMarshalPlain(t *testing.T, k *pbtypes.SigningKeyPrivate) []byte {
	t.Helper()
	_, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{{PublicKey: k.PublicKey, CreatedAt: k.CreatedAt}},
		[]pbtypes.SigningKeyPrivate{*k},
	)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return privJSON
}

// TestEnsureOperatorHasSigningKey_LeavesDistinctKeyAlone pins the fresh-install case:
// an operator that already has its own signing key must keep it. Adopting the identity
// key there would silently undo the separation the whole design depends on.
func TestEnsureOperatorHasSigningKey_LeavesDistinctKeyAlone(t *testing.T) {
	app := newTestApp(t)

	cm := NewManager(app, testOptions())
	if err := cm.InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		t.Fatalf("find operator collection: %v", err)
	}

	pub, priv := pbtypes.NewSigningKeyPair("OSIGNINGPUBLICKEY", "PSIGNINGPRIVATEKEY", "SOSIGNINGSEED")
	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{pub},
		[]pbtypes.SigningKeyPrivate{priv},
	)
	if err != nil {
		t.Fatalf("marshal signing keys: %v", err)
	}

	rec := core.NewRecord(collection)
	rec.Set("name", "fresh-install")
	rec.Set("public_key", "OIDENTITYPUBLICKEY")
	rec.Set("private_key", "PIDENTITYPRIVATEKEY")
	rec.Set("seed", "SOIDENTITYSEED")
	rec.Set("signing_keys", pubJSON)
	rec.Set("signing_keys_private", privJSON)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	if err := cm.ensureOperatorHasSigningKey(); err != nil {
		t.Fatalf("ensureOperatorHasSigningKey: %v", err)
	}

	fresh, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}
	got := pbtypes.RecordToOperatorModel(fresh)
	if keys := got.AllSigningPublicKeys(); len(keys) != 1 || keys[0] != "OSIGNINGPUBLICKEY" {
		t.Errorf("AllSigningPublicKeys() = %v, want the untouched [OSIGNINGPUBLICKEY]", keys)
	}
}

// TestCreateSystemOperatorCollection_IdentityOptional pins the fresh-install shape.
// pb-nats and any consuming app that also declares this collection must agree on the
// required flags; a fresh install that still declares them required would put new
// deployments back in the state ensureOperatorIdentityOptional exists to remove.
func TestCreateSystemOperatorCollection_IdentityOptional(t *testing.T) {
	app := newTestApp(t)

	cm := NewManager(app, testOptions())
	if err := cm.InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		t.Fatalf("find operator collection: %v", err)
	}

	for _, name := range []string{"private_key", "seed"} {
		field, ok := collection.Fields.GetByName(name).(*core.TextField)
		if !ok {
			t.Fatalf("%s is not a text field", name)
		}
		if field.Required {
			t.Errorf("%s is required on a fresh install; identity must be removable", name)
		}
	}
}

// TestEnsureOperatorIdentityOptional_AllowsBlankIdentity reproduces the old collection
// shape and asserts that after the migration an operator record saves with private_key
// and seed blank.
//
// The round-trip is the assertion, not the Required flag. A blank required column fails
// the *whole* record save, so the symptom is an unrelated write to the operator failing
// — and since pb-nats initializes from OnBootstrap, which fires for every command, that
// takes down `migrate` too, leaving no in-band way to relax the flag afterwards.
func TestEnsureOperatorIdentityOptional_AllowsBlankIdentity(t *testing.T) {
	app := newTestApp(t)
	operator := operatorCollectionWithoutSigningKeys(t, app)

	rec := core.NewRecord(operator)
	rec.Set("name", "stone-age.io")
	rec.Set("public_key", "OIDENTITYPUBLICKEY")
	rec.Set("private_key", "PIDENTITYPRIVATEKEY")
	rec.Set("seed", "SOIDENTITYSEED")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	// Precondition: blanking the identity is rejected before the migration. If this
	// ever starts succeeding, PocketBase changed and the test no longer reproduces it.
	rec.Set("seed", "")
	rec.Set("private_key", "")
	if err := app.Save(rec); err == nil {
		t.Fatal("precondition failed: blank required identity fields saved cleanly")
	}

	cm := NewManager(app, pbtypes.Options{})
	if err := cm.ensureOperatorIdentityOptional(); err != nil {
		t.Fatalf("ensureOperatorIdentityOptional: %v", err)
	}

	fresh, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record: %v", err)
	}
	fresh.Set("seed", "")
	fresh.Set("private_key", "")
	if err := app.Save(fresh); err != nil {
		t.Fatalf("save blank identity after migration: %v", err)
	}

	final, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record for assertion: %v", err)
	}
	if got := final.GetString("seed"); got != "" {
		t.Errorf("seed = %q, want empty", got)
	}
	if got := final.GetString("public_key"); got != "OIDENTITYPUBLICKEY" {
		t.Errorf("migration disturbed public_key: %q", got)
	}

	// Idempotent.
	if err := cm.ensureOperatorIdentityOptional(); err != nil {
		t.Fatalf("ensureOperatorIdentityOptional (second run): %v", err)
	}
}
