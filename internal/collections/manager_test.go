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
