package sync

import (
	"os"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// newTestApp spins up a real PocketBase instance backed by a throwaway data dir so
// record semantics are exercised against the real implementation.
func newTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()

	dir, err := os.MkdirTemp("", "pbnats-sync-*")
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

// newAccountsCollection creates a minimal stand-in for the accounts collection,
// carrying only the fields these tests exercise.
func newAccountsCollection(t *testing.T, app *pocketbase.PocketBase) *core.Collection {
	t.Helper()

	accounts := core.NewBaseCollection("nats_accounts")
	accounts.Fields.Add(&core.TextField{Name: "name", Max: 100})
	accounts.Fields.Add(&core.JSONField{Name: "signing_keys", MaxSize: 10000})
	accounts.Fields.Add(&core.BoolField{Name: "active"})
	if err := app.Save(accounts); err != nil {
		t.Fatalf("save accounts collection: %v", err)
	}
	return accounts
}

// TestActiveTransitionSurvivesSave pins the behavior the suspend/reactivate logic
// depends on: a record's Original() keeps the pre-save database state even after the
// save succeeds, so the account hook can still tell an active->inactive edge from an
// ordinary update when it runs in OnRecordAfterUpdateSuccess. PocketBase only
// refreshes that snapshot when a record is scanned back out of the database, but
// nothing in the library guarantees it, so assert it rather than assume it.
func TestActiveTransitionSurvivesSave(t *testing.T) {
	app := newTestApp(t)
	collection := newAccountsCollection(t, app)

	record := core.NewRecord(collection)
	record.Set("name", "tenant-a")
	record.Set("active", true)
	if err := app.Save(record); err != nil {
		t.Fatalf("save account: %v", err)
	}

	// Reload so the original-data snapshot reflects the stored row.
	record, err := app.FindRecordById(collection.Name, record.Id)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	record.Set("active", false)
	if err := app.Save(record); err != nil {
		t.Fatalf("save deactivated account: %v", err)
	}

	if !record.Original().GetBool("active") {
		t.Error("Original().active = false after save, want true (pre-save state)")
	}
	if record.GetBool("active") {
		t.Error("active = true after deactivating, want false")
	}

	// An unrelated update to an already-inactive account must not look like an edge,
	// or every save would re-withdraw the account from NATS.
	record, err = app.FindRecordById(collection.Name, record.Id)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	record.Set("name", "tenant-a-renamed")
	if err := app.Save(record); err != nil {
		t.Fatalf("save renamed account: %v", err)
	}
	if record.Original().GetBool("active") || record.GetBool("active") {
		t.Error("inactive account looks active on an unrelated update")
	}
}

// TestHasPriorState covers the case that makes raw edge detection unsafe: the create
// hooks generate keys and save the record a second time, and that save runs the
// update hooks. PocketBase pre-fills a new record's original data with field zero
// values, so a record created with active=true would otherwise be indistinguishable
// from a genuine inactive->active reactivation.
func TestHasPriorState(t *testing.T) {
	app := newTestApp(t)
	collection := newAccountsCollection(t, app)
	collection.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	if err := app.Save(collection); err != nil {
		t.Fatalf("add public_key field: %v", err)
	}

	// A record still being finished by the create hook: inserted, keys not yet saved.
	record := core.NewRecord(collection)
	record.Set("name", "tenant-new")
	record.Set("active", true)
	if err := app.Save(record); err != nil {
		t.Fatalf("save new account: %v", err)
	}
	if hasPriorState(record) {
		t.Error("hasPriorState = true for a freshly created record, want false")
	}
	if record.Original().GetBool("active") {
		t.Error("precondition failed: expected zero-valued original data on a new record")
	}

	// The create hook's second save persists the generated keys.
	record.Set("public_key", "AK_GENERATED")
	if err := app.Save(record); err != nil {
		t.Fatalf("save account keys: %v", err)
	}

	// Once reloaded, the record has real stored state and edges are meaningful.
	record, err := app.FindRecordById(collection.Name, record.Id)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if !hasPriorState(record) {
		t.Error("hasPriorState = false for a stored record with keys, want true")
	}
}

func TestSigningKeysRemoved(t *testing.T) {
	app := newTestApp(t)
	collection := newAccountsCollection(t, app)

	keyJSON := func(pubKeys ...string) []pbtypes.SigningKeyPublic {
		keys := make([]pbtypes.SigningKeyPublic, 0, len(pubKeys))
		for _, k := range pubKeys {
			keys = append(keys, pbtypes.SigningKeyPublic{PublicKey: k})
		}
		return keys
	}

	// storedAccount returns a freshly loaded record holding the given signing keys,
	// so Original() reflects them.
	storedAccount := func(t *testing.T, pubKeys ...string) *core.Record {
		t.Helper()
		record := core.NewRecord(collection)
		record.Set("name", "tenant")
		record.Set("active", true)
		record.Set("signing_keys", keyJSON(pubKeys...))
		if err := app.Save(record); err != nil {
			t.Fatalf("save account: %v", err)
		}
		loaded, err := app.FindRecordById(collection.Name, record.Id)
		if err != nil {
			t.Fatalf("find account: %v", err)
		}
		return loaded
	}

	t.Run("key removed", func(t *testing.T) {
		record := storedAccount(t, "AK1", "AK2")
		record.Set("signing_keys", keyJSON("AK2"))
		if !signingKeysRemoved(record) {
			t.Error("signingKeysRemoved = false after dropping AK1, want true")
		}
	})

	t.Run("emergency rotation replaces all keys", func(t *testing.T) {
		record := storedAccount(t, "AK1", "AK2")
		record.Set("signing_keys", keyJSON("AK3"))
		if !signingKeysRemoved(record) {
			t.Error("signingKeysRemoved = false after full rotation, want true")
		}
	})

	t.Run("key added", func(t *testing.T) {
		record := storedAccount(t, "AK1")
		record.Set("signing_keys", keyJSON("AK1", "AK2"))
		if signingKeysRemoved(record) {
			t.Error("signingKeysRemoved = true after only adding a key, want false")
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		record := storedAccount(t, "AK1", "AK2")
		record.Set("name", "renamed")
		if signingKeysRemoved(record) {
			t.Error("signingKeysRemoved = true on unrelated update, want false")
		}
	})
}
