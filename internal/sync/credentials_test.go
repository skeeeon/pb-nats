package sync

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// syncFixture is a minimal but real account/role/user set wired to a Manager, for
// the behaviours that only show up in record state.
type syncFixture struct {
	app     *pocketbase.PocketBase
	sm      *Manager
	options pbtypes.Options
	account *core.Record
	role    *core.Record
}

// newSyncFixture builds the three collections the credential paths touch, carrying
// the fields those paths read. The publisher is nil: nothing exercised here queues
// anything, and a fake would only assert its own shape.
func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()

	app := newTestApp(t)
	nm := nkey.NewManager()
	options := pbtypes.Options{
		AccountCollectionName: "nats_accounts",
		RoleCollectionName:    "nats_roles",
		UserCollectionName:    "nats_users",
	}

	accounts := core.NewBaseCollection(options.AccountCollectionName)
	accounts.Fields.Add(&core.TextField{Name: "name", Max: 100})
	accounts.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	accounts.Fields.Add(&core.TextField{Name: "seed", Max: 200})
	accounts.Fields.Add(&core.JSONField{Name: "signing_keys", MaxSize: 10000})
	accounts.Fields.Add(&core.JSONField{Name: "signing_keys_private", MaxSize: 10000})
	accounts.Fields.Add(&core.TextField{Name: "jwt", Max: 50000})
	accounts.Fields.Add(&core.JSONField{Name: "revocations", MaxSize: 50000})
	accounts.Fields.Add(&core.BoolField{Name: "active"})
	if err := app.Save(accounts); err != nil {
		t.Fatalf("save accounts collection: %v", err)
	}

	roles := core.NewBaseCollection(options.RoleCollectionName)
	roles.Fields.Add(&core.TextField{Name: "name", Max: 100})
	roles.Fields.Add(&core.JSONField{Name: "publish_permissions", MaxSize: 5000})
	roles.Fields.Add(&core.JSONField{Name: "subscribe_permissions", MaxSize: 5000})
	if err := app.Save(roles); err != nil {
		t.Fatalf("save roles collection: %v", err)
	}

	users := core.NewBaseCollection(options.UserCollectionName)
	users.Fields.Add(&core.TextField{Name: "nats_username", Max: 100})
	users.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	users.Fields.Add(&core.TextField{Name: "private_key", Max: 200})
	users.Fields.Add(&core.TextField{Name: "seed", Max: 200})
	users.Fields.Add(&core.TextField{Name: "account_id", Max: 200})
	users.Fields.Add(&core.TextField{Name: "role_id", Max: 200})
	users.Fields.Add(&core.TextField{Name: "jwt", Max: 5000})
	users.Fields.Add(&core.TextField{Name: "creds_file", Max: 10000})
	users.Fields.Add(&core.BoolField{Name: "active"})
	if err := app.Save(users); err != nil {
		t.Fatalf("save users collection: %v", err)
	}

	// A real account key pair, so user JWTs can actually be signed.
	seed, public, signingSeed, signingPublic, err := nm.GenerateAccountKeyPair()
	if err != nil {
		t.Fatalf("GenerateAccountKeyPair: %v", err)
	}
	signPub, signPriv := pbtypes.NewSigningKeyPair(signingPublic, "", signingSeed)
	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{signPub},
		[]pbtypes.SigningKeyPrivate{signPriv},
	)
	if err != nil {
		t.Fatalf("MarshalSigningKeys: %v", err)
	}

	account := core.NewRecord(accounts)
	account.Set("name", "Tenant A")
	account.Set("public_key", public)
	account.Set("seed", seed)
	account.Set("signing_keys", pubJSON)
	account.Set("signing_keys_private", privJSON)
	account.Set("active", true)
	if err := app.Save(account); err != nil {
		t.Fatalf("save account: %v", err)
	}

	role := core.NewRecord(roles)
	role.Set("name", "device")
	role.Set("publish_permissions", []string{"telemetry.>"})
	role.Set("subscribe_permissions", []string{"commands.>"})
	if err := app.Save(role); err != nil {
		t.Fatalf("save role: %v", err)
	}

	sm := NewManager(app, jwt.NewGenerator(nm, options), nm, nil, options, "")

	return &syncFixture{app: app, sm: sm, options: options, account: account, role: role}
}

// newUser creates a user with real keys and a real JWT, then overwrites the JWT and
// creds file with a sentinel.
//
// The sentinel is what makes "was this reissued?" answerable. A regeneration inside
// the same wall-clock second produces a byte-identical JWT — nats-io/jwt derives the
// claim id from the claim body and stamps iat at second resolution — so comparing
// the JWT before and after cannot distinguish "reissued" from "left alone".
func (f *syncFixture) newUser(t *testing.T, username string, active bool) *core.Record {
	t.Helper()

	collection, err := f.app.FindCollectionByNameOrId(f.options.UserCollectionName)
	if err != nil {
		t.Fatalf("find users collection: %v", err)
	}

	record := core.NewRecord(collection)
	record.Set("nats_username", username)
	record.Set("account_id", f.account.Id)
	record.Set("role_id", f.role.Id)
	record.Set("active", active)

	if err := f.sm.generateUserKeys(record); err != nil {
		t.Fatalf("generateUserKeys(%s): %v", username, err)
	}
	if record.GetString("jwt") == "" {
		t.Fatalf("user %s got no JWT from generateUserKeys", username)
	}

	record.Set("jwt", "sentinel-jwt")
	record.Set("creds_file", "sentinel-creds")
	if err := f.app.Save(record); err != nil {
		t.Fatalf("save user %s: %v", username, err)
	}
	return record
}

func (f *syncFixture) reload(t *testing.T, id string) *core.Record {
	t.Helper()
	record, err := f.app.FindRecordById(f.options.UserCollectionName, id)
	if err != nil {
		t.Fatalf("reload user %s: %v", id, err)
	}
	return record
}

// TestRegenerateUsersWithRoleSkipsSuspended pins that editing a role cannot hand a
// suspended user working credentials back.
//
// Clearing a user's active flag revokes their key and deliberately does not reissue,
// so they hold nothing NATS will accept. Reissuing them from an unrelated role edit
// stamped a JWT after the revocation cutoff — valid again — and wrote a fresh
// creds_file for them to download, silently undoing the suspension.
// regenerateUsersInAccount already skipped inactive users; this path did not.
func TestRegenerateUsersWithRoleSkipsSuspended(t *testing.T) {
	f := newSyncFixture(t)

	active := f.newUser(t, "alice", true)
	suspended := f.newUser(t, "bob", false)

	if err := f.sm.regenerateUsersWithRole(f.role.Id); err != nil {
		t.Fatalf("regenerateUsersWithRole: %v", err)
	}

	// The suspended user must be untouched: still no usable credentials.
	stored := f.reload(t, suspended.Id)
	if got := stored.GetString("jwt"); got != "sentinel-jwt" {
		t.Errorf("suspended user JWT was reissued — suspension silently undone")
	}
	if got := stored.GetString("creds_file"); got != "sentinel-creds" {
		t.Errorf("suspended user creds_file was rewritten — suspension silently undone")
	}

	// The active user must still be reissued, or the role edit did nothing.
	stored = f.reload(t, active.Id)
	if got := stored.GetString("jwt"); got == "sentinel-jwt" || got == "" {
		t.Errorf("active user JWT = %q, want a freshly issued JWT", got)
	}
	if got := stored.GetString("creds_file"); got == "sentinel-creds" || got == "" {
		t.Error("active user creds_file was not reissued")
	}
}

// TestProtectedDeleteReason pins which deletes are refused. The operator record is
// the one that matters most: its seed is the root of trust and exists nowhere else,
// so deleting it orphans every account JWT and every distributed creds file at once,
// with no repair short of restoring the database.
func TestProtectedDeleteReason(t *testing.T) {
	f := newSyncFixture(t)

	// Treat the fixture's account as the system account.
	f.sm.systemAccountID = f.account.Id

	operatorCollection := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	operatorCollection.Fields.Add(&core.TextField{Name: "name", Max: 100})
	if err := f.app.Save(operatorCollection); err != nil {
		t.Fatalf("save operator collection: %v", err)
	}
	operator := core.NewRecord(operatorCollection)
	operator.Set("name", "pbnats_operator")
	if err := f.app.Save(operator); err != nil {
		t.Fatalf("save operator: %v", err)
	}

	sysUser := f.newUser(t, "sys", true)
	ordinaryUser := f.newUser(t, "alice", true)

	otherAccount := core.NewRecord(f.account.Collection())
	otherAccount.Set("name", "Tenant B")
	otherAccount.Set("active", true)
	if err := f.app.Save(otherAccount); err != nil {
		t.Fatalf("save other account: %v", err)
	}

	unusedRole := core.NewRecord(f.role.Collection())
	unusedRole.Set("name", "unused")
	if err := f.app.Save(unusedRole); err != nil {
		t.Fatalf("save unused role: %v", err)
	}

	tests := []struct {
		name       string
		collection string
		record     *core.Record
		refuse     bool
	}{
		{"system operator", pbtypes.SystemOperatorCollectionName, operator, true},
		{"system account", f.options.AccountCollectionName, f.account, true},
		{"ordinary account", f.options.AccountCollectionName, otherAccount, false},
		{"system user", f.options.UserCollectionName, sysUser, true},
		{"ordinary user", f.options.UserCollectionName, ordinaryUser, false},
		{"role still in use", f.options.RoleCollectionName, f.role, true},
		{"unreferenced role", f.options.RoleCollectionName, unusedRole, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := f.sm.protectedDeleteReason(tt.collection, tt.record)
			if tt.refuse && reason == "" {
				t.Error("delete allowed, want refused")
			}
			if !tt.refuse && reason != "" {
				t.Errorf("delete refused (%q), want allowed", reason)
			}
		})
	}
}
