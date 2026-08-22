package publisher

import (
	"os"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/collections"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// newTestApp spins up a real PocketBase instance backed by a throwaway data dir.
func newTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()

	dir, err := os.MkdirTemp("", "pbnats-publisher-*")
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

// newQueueFixture builds the real collections rather than a hand-rolled queue
// schema, so a field the queue code writes but the collection does not declare
// shows up here instead of being silently discarded.
func newQueueFixture(t *testing.T) *Manager {
	t.Helper()

	app := newTestApp(t)
	options := pbtypes.Options{
		AccountCollectionName: pbtypes.DefaultAccountCollectionName,
		RoleCollectionName:    pbtypes.DefaultRoleCollectionName,
		UserCollectionName:    pbtypes.DefaultUserCollectionName,
		ExportCollectionName:  pbtypes.DefaultExportCollectionName,
		ImportCollectionName:  pbtypes.DefaultImportCollectionName,
	}
	if err := collections.NewManager(app, options).InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}

	return NewManager(app, options)
}

// newFailedQueueRecord creates a queue record already marked permanently failed.
func newFailedQueueRecord(t *testing.T, p *Manager, action, publicKey string) *core.Record {
	t.Helper()

	collection, err := p.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err != nil {
		t.Fatalf("find publish queue collection: %v", err)
	}

	record := core.NewRecord(collection)
	record.Set("account_id", "acct_"+action+publicKey)
	record.Set("account_public_key", publicKey)
	record.Set("account_name", "tenant")
	record.Set("action", action)
	record.Set("attempts", pbtypes.MaxQueueAttempts)
	record.Set("message", "Exceeded maximum retry attempts")
	record.Set("failed_at", time.Now())
	if err := p.app.Save(record); err != nil {
		t.Fatalf("save queue record: %v", err)
	}
	return record
}

func reloadQueueRecord(t *testing.T, p *Manager, id string) *core.Record {
	t.Helper()
	record, err := p.app.FindRecordById(pbtypes.PublishQueueCollectionName, id)
	if err != nil {
		t.Fatalf("reload queue record %s: %v", id, err)
	}
	return record
}

// TestReviveFailedDeletes pins that a restart retries account deletions that were
// given up on, and only those.
//
// A failed delete is the one queue operation nothing else can reconstruct:
// ReconcileAccounts re-enqueues active accounts at boot, but a delete's account row
// is already gone, so the queue record's public key snapshot is the only surviving
// record of the intent. Left dead and then swept by CleanupFailedRecords, the
// account stays live in NATS forever with nothing left to notice it by.
func TestReviveFailedDeletes(t *testing.T) {
	p := newQueueFixture(t)

	deletion := newFailedQueueRecord(t, p, pbtypes.PublishActionDelete, "ADELETEDACCOUNTKEY")
	noSnapshot := newFailedQueueRecord(t, p, pbtypes.PublishActionDelete, "")
	upsert := newFailedQueueRecord(t, p, pbtypes.PublishActionUpsert, "AUPSERTACCOUNTKEY")

	if err := p.ReviveFailedDeletes(); err != nil {
		t.Fatalf("ReviveFailedDeletes: %v", err)
	}

	revived := reloadQueueRecord(t, p, deletion.Id)
	if !revived.GetDateTime("failed_at").IsZero() {
		t.Error("delete record still marked failed, want the mark cleared")
	}
	if got := revived.GetInt("attempts"); got != 0 {
		t.Errorf("revived delete attempts = %d, want 0", got)
	}

	// Clearing the mark only helps if the processor's own filter then matches the
	// row. That filter tests for NULL or empty string, so how PocketBase stores a
	// cleared date field is load-bearing rather than cosmetic.
	pending, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName,
		dbx.Or(
			dbx.NewExp("failed_at IS NULL"),
			dbx.HashExp{"failed_at": ""},
		))
	if err != nil {
		t.Fatalf("query pending records: %v", err)
	}
	found := false
	for _, record := range pending {
		if record.Id == deletion.Id {
			found = true
		}
		if record.Id == noSnapshot.Id || record.Id == upsert.Id {
			t.Errorf("record %s is pending, want it left failed", record.Id)
		}
	}
	if !found {
		t.Error("revived delete is not visible to the queue processor's pending query")
	}

	// A delete with no public key snapshot cannot be published at all: the account
	// row is gone, so the key is unrecoverable. Reviving it would only burn retries.
	stale := reloadQueueRecord(t, p, noSnapshot.Id)
	if stale.GetDateTime("failed_at").IsZero() {
		t.Error("delete without a public key snapshot was revived, want it left failed")
	}

	// Upserts are reconciliation's job, not this one's.
	stillFailed := reloadQueueRecord(t, p, upsert.Id)
	if stillFailed.GetDateTime("failed_at").IsZero() {
		t.Error("failed upsert was revived, want it left to ReconcileAccounts")
	}
	if got := stillFailed.GetInt("attempts"); got != pbtypes.MaxQueueAttempts {
		t.Errorf("failed upsert attempts = %d, want %d", got, pbtypes.MaxQueueAttempts)
	}
}
