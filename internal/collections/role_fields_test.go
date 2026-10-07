package collections

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// TestEnsureRoleFields_LegacyShapes builds the roles collection in each shape that has
// shipped without the current fields, runs the real InitializeCollections, and asserts
// that every role field written afterwards survives a save and reload, read back through
// RecordToRoleModel.
//
// The round-trip is the point. PocketBase discards writes to undeclared fields without
// erroring, so asserting that a field exists would pass against a collection that still
// drops the value. Going through InitializeCollections rather than calling
// ensureRoleFields directly also pins the wiring: a pass that exists but never runs is
// the same bug.
func TestEnsureRoleFields_LegacyShapes(t *testing.T) {
	jsonField := func(name string) core.Field { return &core.JSONField{Name: name, MaxSize: 5000} }
	numberField := func(name string) core.Field {
		return &core.NumberField{Name: name, OnlyInt: true, Min: types.Pointer(-1.0)}
	}
	identity := []core.Field{
		&core.TextField{Name: "name", Required: true, Max: 100},
		&core.TextField{Name: "description", Max: 500},
		&core.BoolField{Name: "is_default"},
	}

	for _, tc := range []struct {
		name   string
		fields []core.Field
	}{
		{
			// 48aa9b3: max_connections where max_subscriptions is now; no deny, no response.
			name: "2025-05 max_connections",
			fields: []core.Field{
				jsonField("publish_permissions"), jsonField("subscribe_permissions"),
				numberField("max_connections"), numberField("max_data"), numberField("max_payload"),
			},
		},
		{
			// 33b69d6: limits renamed; still no deny lists and no response fields.
			name: "2025-08 no deny or response",
			fields: []core.Field{
				jsonField("publish_permissions"), jsonField("subscribe_permissions"),
				numberField("max_subscriptions"), numberField("max_data"), numberField("max_payload"),
			},
		},
		{
			// A consumer-defined schema (the platform's) with every field except allow_response*.
			name: "consumer schema without response fields",
			fields: []core.Field{
				jsonField("publish_permissions"), jsonField("subscribe_permissions"),
				jsonField("publish_deny_permissions"), jsonField("subscribe_deny_permissions"),
				numberField("max_subscriptions"), numberField("max_data"), numberField("max_payload"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			opts := testOptions()

			legacy := core.NewBaseCollection(opts.RoleCollectionName)
			for _, f := range append(append([]core.Field{}, identity...), tc.fields...) {
				legacy.Fields.Add(f)
			}
			if err := app.Save(legacy); err != nil {
				t.Fatalf("save legacy roles collection: %v", err)
			}

			rec := core.NewRecord(legacy)
			rec.Set("name", "responder")
			rec.Set("publish_permissions", []string{"svc.>"})
			rec.Set("max_data", 7)
			if legacy.Fields.GetByName("max_connections") != nil {
				rec.Set("max_connections", 3)
			}
			// Precondition: allow_response is missing from every legacy shape, so this
			// write is dropped. If it persists, PocketBase changed and the test no longer
			// reproduces anything.
			rec.Set("allow_response", true)
			if err := app.Save(rec); err != nil {
				t.Fatalf("save legacy role: %v", err)
			}
			if got := reload(t, app, opts.RoleCollectionName, rec.Id).GetBool("allow_response"); got {
				t.Fatal("precondition failed: undeclared allow_response persisted")
			}

			cm := NewManager(app, opts)
			if err := cm.InitializeCollections(); err != nil {
				t.Fatalf("InitializeCollections: %v", err)
			}

			role := reload(t, app, opts.RoleCollectionName, rec.Id)
			role.Set("publish_deny_permissions", []string{"svc.admin.>"})
			role.Set("subscribe_deny_permissions", []string{"svc.audit.>"})
			role.Set("allow_response", true)
			role.Set("allow_response_max", -1)
			role.Set("allow_response_ttl", 30)
			role.Set("max_subscriptions", 50)
			mustSave(t, app, role)

			final := reload(t, app, opts.RoleCollectionName, rec.Id)
			model := pbtypes.RecordToRoleModel(final)
			if !model.AllowResponse || model.AllowResponseMax != -1 || model.AllowResponseTTL != 30 {
				t.Errorf("response = (%v, %d, %d), want (true, -1, 30)",
					model.AllowResponse, model.AllowResponseMax, model.AllowResponseTTL)
			}
			assertJSONEqual(t, "publish_deny_permissions", model.PublishDenyPermissions, `["svc.admin.>"]`)
			assertJSONEqual(t, "subscribe_deny_permissions", model.SubscribeDenyPermissions, `["svc.audit.>"]`)
			if model.MaxSubscriptions != 50 {
				t.Errorf("MaxSubscriptions = %d, want 50", model.MaxSubscriptions)
			}

			// Existing data is untouched, including a legacy field roleFields does not know.
			assertJSONEqual(t, "publish_permissions", model.PublishPermissions, `["svc.>"]`)
			if model.MaxData != 7 {
				t.Errorf("MaxData = %d, want 7", model.MaxData)
			}
			if legacy.Fields.GetByName("max_connections") != nil && final.GetInt("max_connections") != 3 {
				t.Errorf("max_connections = %d, want 3 (legacy data must survive)", final.GetInt("max_connections"))
			}

			// Idempotent: a second boot changes nothing and does not fail.
			if err := cm.InitializeCollections(); err != nil {
				t.Fatalf("InitializeCollections (second run): %v", err)
			}
		})
	}
}

// A fresh install and an upgraded one must end up with the same role fields, or the
// next field added to createRolesCollection alone reopens this gap.
func TestEnsureRoleFields_FreshInstallHasAllRoleFields(t *testing.T) {
	app, opts := newBoundaryFixture(t, "")
	roles := mustCollection(t, app, opts.RoleCollectionName)
	for _, field := range roleFields() {
		got := roles.Fields.GetByName(field.GetName())
		if got == nil {
			t.Errorf("fresh roles collection missing %q", field.GetName())
		} else if got.Type() != field.Type() {
			t.Errorf("%s type = %q, want %q", field.GetName(), got.Type(), field.Type())
		}
	}
}
