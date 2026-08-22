package collections

import (
	"os"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// newTestApp spins up a real PocketBase instance backed by a throwaway data dir
// so migrations run against the same validation logic production uses.
func newTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()

	dir, err := os.MkdirTemp("", "pbnats-collections-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  dir,
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { app.ResetBootstrapState() })

	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return app
}

// TestEnsureSystemOperatorFields_AddsSystemAccountID reproduces an operator
// collection created before system_account_id existed, then runs the migration and
// asserts the field is added AND that a write to it survives a save.
//
// The second assertion is the point. PocketBase discards writes to fields a
// collection does not declare without erroring, so the bug this migration fixes
// looked like a successful save on every boot while the value stayed empty — and the
// only visible symptom was pb-nats sitting in bootstrap mode forever, because
// publisher.getSystemUser cannot resolve the system account without this id.
// Asserting the field exists would pass against a collection that still drops the
// value; asserting a round-trip is what actually pins the behaviour.
func TestEnsureSystemOperatorFields_AddsSystemAccountID(t *testing.T) {
	app := newTestApp(t)

	// Legacy operator collection: everything except system_account_id.
	operator := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	operator.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
	operator.Fields.Add(&core.TextField{Name: "public_key", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "private_key", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "seed", Required: true, Max: 200})
	operator.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	if err := app.Save(operator); err != nil {
		t.Fatalf("save legacy operator collection: %v", err)
	}

	rec := core.NewRecord(operator)
	rec.Set("name", "stone-age.io")
	rec.Set("public_key", "OTESTOPERATORPUBLICKEY")
	rec.Set("private_key", "PTESTOPERATORPRIVATEKEY")
	rec.Set("seed", "SOTESTOPERATORSEED")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record: %v", err)
	}

	// Precondition: the write is silently dropped before the migration. If this ever
	// starts persisting, PocketBase changed and the whole failure mode is gone.
	rec.Set("system_account_id", "acct000000000001")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save operator record with unknown field: %v", err)
	}
	if reloaded, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id); err != nil {
		t.Fatalf("reload operator record: %v", err)
	} else if got := reloaded.GetString("system_account_id"); got != "" {
		t.Fatalf("precondition failed: undeclared field persisted %q; this test no longer reproduces the bug", got)
	}

	// Run the real migration.
	cm := NewManager(app, pbtypes.Options{})
	if err := cm.ensureSystemOperatorFields(); err != nil {
		t.Fatalf("ensureSystemOperatorFields: %v", err)
	}

	got, err := app.FindCollectionByNameOrId(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		t.Fatalf("reload operator collection: %v", err)
	}
	field := got.Fields.GetByName("system_account_id")
	if field == nil {
		t.Fatal("system_account_id field was not added")
	}
	if field.Type() != core.FieldTypeText {
		t.Fatalf("system_account_id type = %q, want %q", field.Type(), core.FieldTypeText)
	}

	// The value must now round-trip. Existing fields and their data are untouched.
	fresh, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record after migration: %v", err)
	}
	fresh.Set("system_account_id", "acct000000000001")
	if err := app.Save(fresh); err != nil {
		t.Fatalf("save system_account_id after migration: %v", err)
	}
	final, err := app.FindRecordById(pbtypes.SystemOperatorCollectionName, rec.Id)
	if err != nil {
		t.Fatalf("reload operator record for assertion: %v", err)
	}
	if final.GetString("system_account_id") != "acct000000000001" {
		t.Errorf("system_account_id = %q, want %q", final.GetString("system_account_id"), "acct000000000001")
	}
	if final.GetString("public_key") != "OTESTOPERATORPUBLICKEY" {
		t.Errorf("migration disturbed public_key: %q", final.GetString("public_key"))
	}

	// Running the migration again must be a no-op (idempotent).
	if err := cm.ensureSystemOperatorFields(); err != nil {
		t.Fatalf("ensureSystemOperatorFields (second run): %v", err)
	}
}

// TestEnsurePublishQueueFields_RelationToText reproduces a pre-v1.3.0 install
// where nats_publish_queue.account_id is a cascade-deleting relation, then runs
// the migration and asserts it converts the field to text (the old same-id
// re-add tripped PocketBase's "Field type cannot be changed" validation) and
// adds the account snapshot fields.
func TestEnsurePublishQueueFields_RelationToText(t *testing.T) {
	app := newTestApp(t)

	// Relation target — stands in for the accounts collection.
	accounts := core.NewBaseCollection("nats_accounts")
	accounts.Fields.Add(&core.TextField{Name: "name", Max: 100})
	if err := app.Save(accounts); err != nil {
		t.Fatalf("save accounts: %v", err)
	}

	// Legacy publish queue: account_id is a cascade-deleting relation.
	pq := core.NewBaseCollection(pbtypes.PublishQueueCollectionName)
	pq.Fields.Add(&core.RelationField{
		Name: "account_id", Required: true, MaxSelect: 1,
		CollectionId: accounts.Id, CascadeDelete: true,
	})
	pq.Fields.Add(&core.SelectField{
		Name: "action", Required: true, MaxSelect: 1,
		Values: []string{pbtypes.PublishActionUpsert, pbtypes.PublishActionDelete},
	})
	if err := app.Save(pq); err != nil {
		t.Fatalf("save legacy publish queue: %v", err)
	}

	// A pending queue row referencing a real account.
	acc := core.NewRecord(accounts)
	acc.Set("name", "acme")
	if err := app.Save(acc); err != nil {
		t.Fatalf("save account record: %v", err)
	}
	row := core.NewRecord(pq)
	row.Set("account_id", acc.Id)
	row.Set("action", pbtypes.PublishActionUpsert)
	if err := app.Save(row); err != nil {
		t.Fatalf("save queue row: %v", err)
	}

	// Run the real migration.
	cm := NewManager(app, pbtypes.Options{})
	if err := cm.ensurePublishQueueFields(); err != nil {
		t.Fatalf("ensurePublishQueueFields: %v", err)
	}

	got, err := app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err != nil {
		t.Fatalf("reload publish queue: %v", err)
	}

	accountID := got.Fields.GetByName("account_id")
	if accountID == nil {
		t.Fatal("account_id field is missing after migration")
	}
	if accountID.Type() != core.FieldTypeText {
		t.Fatalf("account_id type = %q, want %q", accountID.Type(), core.FieldTypeText)
	}
	if got.Fields.GetByName("account_public_key") == nil {
		t.Error("account_public_key field was not added")
	}
	if got.Fields.GetByName("account_name") == nil {
		t.Error("account_name field was not added")
	}

	// Running the migration again must be a no-op (idempotent).
	if err := cm.ensurePublishQueueFields(); err != nil {
		t.Fatalf("ensurePublishQueueFields (second run): %v", err)
	}
}
