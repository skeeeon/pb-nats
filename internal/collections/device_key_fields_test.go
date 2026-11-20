package collections

import (
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// TestEnsureUserDeviceKeyFields builds a nats_users collection from before
// device-held keys, runs the migration, and asserts both halves by behaviour: a
// device_key write survives a save (PocketBase drops writes to undeclared fields
// without erroring), and a second row with the same public_key is refused while
// two empty ones are not.
func TestEnsureUserDeviceKeyFields(t *testing.T) {
	app := newTestApp(t)

	legacy := core.NewBaseCollection("nats_users")
	legacy.Fields.Add(&core.TextField{Name: "nats_username", Max: 100})
	legacy.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	if err := app.Save(legacy); err != nil {
		t.Fatalf("save legacy users collection: %v", err)
	}

	cm := NewManager(app, pbtypes.Options{UserCollectionName: "nats_users"})
	if err := cm.ensureUserDeviceKeyFields(); err != nil {
		t.Fatalf("ensureUserDeviceKeyFields: %v", err)
	}
	// Idempotent: a second boot changes nothing and does not fail.
	if err := cm.ensureUserDeviceKeyFields(); err != nil {
		t.Fatalf("ensureUserDeviceKeyFields, second run: %v", err)
	}

	users, err := app.FindCollectionByNameOrId("nats_users")
	if err != nil {
		t.Fatalf("find users collection: %v", err)
	}

	save := func(name, pub string, deviceKey bool) (*core.Record, error) {
		r := core.NewRecord(users)
		r.Set("nats_username", name)
		r.Set("public_key", pub)
		r.Set("device_key", deviceKey)
		return r, app.Save(r)
	}

	r, err := save("a", "UKEYONE", true)
	if err != nil {
		t.Fatalf("save first row: %v", err)
	}
	stored, err := app.FindRecordById("nats_users", r.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !stored.GetBool("device_key") {
		t.Error("device_key did not survive a save: the field was not added")
	}

	if _, err := save("b", "UKEYONE", true); err == nil {
		t.Error("a second row with the same public_key saved, want it refused")
	}
	if _, err := save("c", "", true); err != nil {
		t.Errorf("first row with an empty public_key: %v", err)
	}
	if _, err := save("d", "", true); err != nil {
		t.Errorf("second row with an empty public_key refused, want two waiting rows allowed: %v", err)
	}
}
