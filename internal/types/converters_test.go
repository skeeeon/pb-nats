package types

import (
	"os"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// newTestApp spins up a real PocketBase instance backed by a throwaway data dir so
// field normalization and storage behave exactly as they do in production.
func newTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()

	dir, err := os.MkdirTemp("", "pbnats-types-*")
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

// newUsersCollection creates a stand-in for the users collection carrying only the
// fields this test exercises. A base collection rather than an auth one: the
// converter reads none of the auth fields, and a DateField normalizes identically
// on either.
func newUsersCollection(t *testing.T, app *pocketbase.PocketBase) *core.Collection {
	t.Helper()

	users := core.NewBaseCollection("nats_users")
	users.Fields.Add(&core.TextField{Name: "nats_username", Max: 100})
	users.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	users.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	users.Fields.Add(&core.DateField{Name: "jwt_expires_at"})
	users.Fields.Add(&core.BoolField{Name: "active"})
	if err := app.Save(users); err != nil {
		t.Fatalf("save users collection: %v", err)
	}
	return users
}

// TestRecordToUserModelJWTExpiresAt pins that a per-user expiry actually reaches the
// model, both on the in-flight record a save hook sees and after a database
// round-trip.
//
// The converter did not read jwt_expires_at at all, so the generator's
// `user.JWTExpiresAt != nil` branch could never fire: a per-user date silently did
// nothing and DefaultJWTExpiry always won, despite the field being documented as
// taking precedence. The generator's own expiry test kept passing because it sets
// the model field directly, one layer below the gap — which is why this assertion
// belongs here, on the record-to-model boundary.
func TestRecordToUserModelJWTExpiresAt(t *testing.T) {
	app := newTestApp(t)
	users := newUsersCollection(t, app)

	expiry := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)

	record := core.NewRecord(users)
	record.Set("nats_username", "alice")
	record.Set("active", true)
	record.Set("jwt_expires_at", expiry.Format(time.RFC3339))

	// The in-flight record is what a save hook converts, so it has to work before
	// the row is ever written.
	user := RecordToUserModel(record)
	if user.JWTExpiresAt == nil {
		t.Fatal("JWTExpiresAt nil on the in-flight record, want the set expiry")
	}
	if got := user.JWTExpiresAt.Unix(); got != expiry.Unix() {
		t.Errorf("JWTExpiresAt = %d, want %d", got, expiry.Unix())
	}

	if err := app.Save(record); err != nil {
		t.Fatalf("save user: %v", err)
	}

	stored, err := app.FindRecordById(users.Name, record.Id)
	if err != nil {
		t.Fatalf("refetch user: %v", err)
	}
	user = RecordToUserModel(stored)
	if user.JWTExpiresAt == nil {
		t.Fatal("JWTExpiresAt nil after round-trip, want the stored expiry")
	}
	if got := user.JWTExpiresAt.Unix(); got != expiry.Unix() {
		t.Errorf("JWTExpiresAt after round-trip = %d, want %d", got, expiry.Unix())
	}
}

// TestRecordToUserModelNoExpiry pins that an unset date stays a nil pointer. A zero
// time would read as "expired at the epoch" and the generator would stamp every
// user JWT with it, so the distinction is load-bearing.
func TestRecordToUserModelNoExpiry(t *testing.T) {
	app := newTestApp(t)
	users := newUsersCollection(t, app)

	record := core.NewRecord(users)
	record.Set("nats_username", "bob")
	record.Set("active", true)

	if user := RecordToUserModel(record); user.JWTExpiresAt != nil {
		t.Errorf("JWTExpiresAt = %v, want nil for an unset date", *user.JWTExpiresAt)
	}

	if err := app.Save(record); err != nil {
		t.Fatalf("save user: %v", err)
	}
	stored, err := app.FindRecordById(users.Name, record.Id)
	if err != nil {
		t.Fatalf("refetch user: %v", err)
	}
	if user := RecordToUserModel(stored); user.JWTExpiresAt != nil {
		t.Errorf("JWTExpiresAt after round-trip = %v, want nil", *user.JWTExpiresAt)
	}
}
