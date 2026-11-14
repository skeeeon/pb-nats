package pbnats

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/nats-io/nats-server/v2/server"

	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// An inactive account must stay off the NATS server, whatever change in it
// queues a publish.
//
// Suspending an account removes its JWT from the server's resolver. Every user,
// export and revocation change then queues an upsert for the account without
// asking whether it is active, and the queue used to publish it, which put the
// suspended account straight back. In a multi-tenant consumer that meant any
// member of a suspended tenant could undo the suspension by rotating their own
// credentials.
//
// This runs the whole path for real: pb-nats bootstraps its operator, a
// nats-server starts on the config `nats export` writes for it, and the
// assertions read the server's own resolver directory. Every "absent" is paired
// with a "present" on the same account, so a publisher that never ran cannot
// pass.
func TestASuspendedAccountStaysOffTheServer(t *testing.T) {
	port := freePort(t)

	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	opts := DefaultOptions()
	opts.LogToConsole = false
	opts.NATSServerURL = fmt.Sprintf("nats://127.0.0.1:%d", port)
	opts.PublishQueueInterval = 100 * time.Millisecond
	opts.DebounceInterval = 20 * time.Millisecond
	// The publisher starts before the server exists, and retries the connection
	// every FailbackInterval (default 30s, never under 10s). Take the minimum.
	retry := *pbtypes.DefaultRetryConfig()
	retry.FailbackInterval = 10 * time.Second
	opts.ConnectionRetryConfig = &retry
	if err := Setup(app, opts); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// Registered after TempDir, so it runs first: close the database before the
	// directory holding it is removed (Windows will not delete an open file).
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	jwtDir := startExportedServer(t, app, port)

	accountID, accountPub := createAccount(t, app, "tenant-a")
	roleID := createRole(t, app, "tenant-a-role")
	userID := createUser(t, app, "tenant-a-user", accountID, roleID)

	// PRESENT: an active account is published. Without this, every assertion
	// below would pass against a publisher that never connected.
	waitForAccountFile(t, jwtDir, accountPub, true, "an active account to be published")

	// ABSENT: suspending it removes it.
	setField(t, app, opts.AccountCollectionName, accountID, "active", false)
	waitForAccountFile(t, jwtDir, accountPub, false, "the suspended account to be removed")

	// ABSENT: a user change in the suspended account queues an upsert. It must
	// not put the account back. Wait for the queue to drain, so the upsert has
	// been processed rather than merely not yet reached.
	setField(t, app, opts.UserCollectionName, userID, "description", "changed while suspended")
	waitForEmptyQueue(t, app)
	if accountFileExists(jwtDir, accountPub) {
		t.Fatal("a user change in a suspended account published the account again: the suspension is undone")
	}

	// PRESENT: reactivating publishes it again.
	setField(t, app, opts.AccountCollectionName, accountID, "active", true)
	waitForAccountFile(t, jwtDir, accountPub, true, "the reactivated account to be published")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startExportedServer writes the files `nats export` writes, starts a
// nats-server on them, and returns its resolver directory.
func startExportedServer(t *testing.T, app *pocketbase.PocketBase, port int) string {
	t.Helper()
	operator, sysAccount, err := getOperatorAndSystemAccount(app, pbtypes.DefaultAccountCollectionName, "")
	if err != nil {
		t.Fatalf("read operator and system account: %v", err)
	}
	dir := t.TempDir()
	if err := exportAllFiles(dir, operator, sysAccount, natsConfParams{
		ServerName: "suspension-test", Port: port, JetstreamStoreDir: "js",
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	sopts, err := server.ProcessConfigFile(filepath.Join(dir, "nats.conf"))
	if err != nil {
		t.Fatalf("nats-server cannot parse the exported nats.conf: %v", err)
	}
	sopts.Host = "127.0.0.1"
	sopts.Port = port
	sopts.Websocket = server.WebsocketOpts{} // no browser listener in a test
	sopts.NoLog, sopts.NoSigs = true, true

	srv, err := server.NewServer(sopts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not become ready")
	}
	t.Cleanup(srv.Shutdown)
	return filepath.Join(dir, "jwt")
}

func createAccount(t *testing.T, app core.App, name string) (id, publicKey string) {
	t.Helper()
	rec := newRecord(t, app, pbtypes.DefaultAccountCollectionName)
	rec.Set("name", name)
	rec.Set("active", true)
	if err := app.Save(rec); err != nil {
		t.Fatalf("create account: %v", err)
	}
	fresh, err := app.FindRecordById(pbtypes.DefaultAccountCollectionName, rec.Id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetString("public_key") == "" {
		t.Fatal("the account was created without a public key; the create hook did not run")
	}
	return rec.Id, fresh.GetString("public_key")
}

func createRole(t *testing.T, app core.App, name string) string {
	t.Helper()
	rec := newRecord(t, app, pbtypes.DefaultRoleCollectionName)
	rec.Set("name", name)
	if err := app.Save(rec); err != nil {
		t.Fatalf("create role: %v", err)
	}
	return rec.Id
}

func createUser(t *testing.T, app core.App, username, accountID, roleID string) string {
	t.Helper()
	rec := newRecord(t, app, pbtypes.DefaultUserCollectionName)
	rec.SetEmail(username + "@example.com")
	rec.SetPassword("password123")
	rec.Set("nats_username", username)
	rec.Set("account_id", accountID)
	rec.Set("role_id", roleID)
	rec.Set("active", true)
	if err := app.Save(rec); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return rec.Id
}

func newRecord(t *testing.T, app core.App, collection string) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatal(err)
	}
	return core.NewRecord(c)
}

// setField reloads the record first, so the hooks compare against the stored
// state rather than an earlier in-memory one.
func setField(t *testing.T, app core.App, collection, id, field string, value any) {
	t.Helper()
	rec, err := app.FindRecordById(collection, id)
	if err != nil {
		t.Fatal(err)
	}
	rec.Set(field, value)
	if err := app.Save(rec); err != nil {
		t.Fatalf("set %s.%s: %v", collection, field, err)
	}
}

func accountFileExists(jwtDir, publicKey string) bool {
	_, err := os.Stat(filepath.Join(jwtDir, publicKey+".jwt"))
	return err == nil
}

// waitForAccountFile allows for the bootstrap retry: the publisher starts
// before the server exists and retries every 10 seconds.
func waitForAccountFile(t *testing.T, jwtDir, publicKey string, want bool, what string) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if accountFileExists(jwtDir, publicKey) == want {
			t.Logf("%s: %v", what, time.Since(start).Round(100*time.Millisecond))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitForEmptyQueue(t *testing.T, app core.App) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := app.FindAllRecords(pbtypes.PublishQueueCollectionName,
			dbx.Or(dbx.NewExp("failed_at IS NULL"), dbx.HashExp{"failed_at": ""}))
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the publish queue to drain")
}
