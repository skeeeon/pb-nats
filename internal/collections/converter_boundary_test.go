package collections

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Record → model boundary tests, one per converter.
//
// Three separate defects have now landed in the gap these cover, and each was invisible
// from either side of it: jwt_expires_at (the collection declared it, the converter never
// read it), system_account_id (the converter wrote it, the collection had never declared
// it so PocketBase discarded the value without erroring), and signing_keys_private (both
// sides agreed on the name, but the reader type-asserted a Go string against a field
// PocketBase hands back as types.JSONRaw). A unit test on either half passes through all
// three.
//
// They live in the collections package on purpose. The invariant is a contract *between*
// the collection definitions and the converters, so the test has to build collections the
// way production builds them — InitializeCollections, not a hand-rolled core.Collection.
// A hand-rolled one is free to declare a field production does not, which is precisely
// how the system_account_id defect stayed hidden. types cannot import collections, so
// this direction is also the only one without an import cycle.
//
// Each test sets every field the converter reads to a distinctive value, saves, reloads
// from the database, and asserts the model. The save and reload are load-bearing: an
// in-memory record skips PocketBase's field normalization and storage round-trip, which
// is where all three defects lived.

const boundaryEncryptionKey = "0123456789abcdef0123456789abcdef" // 32 chars

// newBoundaryFixture builds every collection exactly as production does.
func newBoundaryFixture(t *testing.T, encryptionKey string) (*pocketbase.PocketBase, pbtypes.Options) {
	t.Helper()

	app := newTestApp(t)
	opts := testOptions()
	opts.EncryptionKey = encryptionKey

	cm := NewManager(app, opts)
	if err := cm.InitializeCollections(); err != nil {
		t.Fatalf("InitializeCollections: %v", err)
	}
	return app, opts
}

func mustCollection(t *testing.T, app *pocketbase.PocketBase, name string) *core.Collection {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatalf("find collection %s: %v", name, err)
	}
	return collection
}

func mustSave(t *testing.T, app *pocketbase.PocketBase, record *core.Record) {
	t.Helper()
	if err := app.Save(record); err != nil {
		t.Fatalf("save record: %v", err)
	}
}

// reload fetches the record back from the database, discarding the in-memory copy.
func reload(t *testing.T, app *pocketbase.PocketBase, collection, id string) *core.Record {
	t.Helper()
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		t.Fatalf("reload %s/%s: %v", collection, id, err)
	}
	return record
}

func assertJSONEqual(t *testing.T, label string, got json.RawMessage, want string) {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Errorf("%s: not valid JSON (%q): %v", label, string(got), err)
		return
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("%s: bad expectation %q: %v", label, want, err)
	}
	gotNorm, _ := json.Marshal(gotVal)
	wantNorm, _ := json.Marshal(wantVal)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("%s = %s, want %s", label, gotNorm, wantNorm)
	}
}

// seedAccount creates a minimal account and returns its record id, for the relation
// fields on users, exports and imports.
func seedAccount(t *testing.T, app *pocketbase.PocketBase, opts pbtypes.Options, name string) string {
	t.Helper()
	record := core.NewRecord(mustCollection(t, app, opts.AccountCollectionName))
	record.Set("name", name)
	record.Set("public_key", "AACCOUNTPUBLICKEY")
	mustSave(t, app, record)
	return record.Id
}

// seedRole creates a minimal role and returns its record id.
func seedRole(t *testing.T, app *pocketbase.PocketBase, opts pbtypes.Options, name string) string {
	t.Helper()
	record := core.NewRecord(mustCollection(t, app, opts.RoleCollectionName))
	record.Set("name", name)
	mustSave(t, app, record)
	return record.Id
}

// TestRecordToOperatorModel_Boundary covers every field on the operator, under both
// encryption settings. system_account_id is the field a previous defect silently dropped.
func TestRecordToOperatorModel_Boundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"plaintext", ""},
		{"encrypted", boundaryEncryptionKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newBoundaryFixture(t, tc.key)

			pub, priv := pbtypes.NewSigningKeyPair("OSIGNINGPUB", "PSIGNINGPRIV", "SOSIGNINGSEED")
			pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
				[]pbtypes.SigningKeyPublic{pub}, []pbtypes.SigningKeyPrivate{priv})
			if err != nil {
				t.Fatalf("marshal signing keys: %v", err)
			}

			record := core.NewRecord(mustCollection(t, app, pbtypes.SystemOperatorCollectionName))
			record.Set("name", "boundary-operator")
			record.Set("public_key", "OIDENTITYPUB")
			record.Set("jwt", "eyJOPERATOR")
			record.Set("system_account_id", "acct000000000001")
			record.Set("signing_keys", pubJSON)
			if err := pbtypes.EncryptAndSet(record, "private_key", "PIDENTITYPRIV", tc.key); err != nil {
				t.Fatalf("encrypt private_key: %v", err)
			}
			if err := pbtypes.EncryptAndSet(record, "seed", "SOIDENTITYSEED", tc.key); err != nil {
				t.Fatalf("encrypt seed: %v", err)
			}
			if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, tc.key); err != nil {
				t.Fatalf("encrypt signing_keys_private: %v", err)
			}
			mustSave(t, app, record)

			got := pbtypes.RecordToOperatorModel(
				reload(t, app, pbtypes.SystemOperatorCollectionName, record.Id), tc.key)

			if got.ID != record.Id {
				t.Errorf("ID = %q, want %q", got.ID, record.Id)
			}
			if got.Name != "boundary-operator" {
				t.Errorf("Name = %q", got.Name)
			}
			if got.PublicKey != "OIDENTITYPUB" {
				t.Errorf("PublicKey = %q", got.PublicKey)
			}
			if got.PrivateKey != "PIDENTITYPRIV" {
				t.Errorf("PrivateKey = %q, want decrypted identity private key", got.PrivateKey)
			}
			if got.Seed != "SOIDENTITYSEED" {
				t.Errorf("Seed = %q, want decrypted identity seed", got.Seed)
			}
			if got.JWT != "eyJOPERATOR" {
				t.Errorf("JWT = %q", got.JWT)
			}
			if got.SystemAccountID != "acct000000000001" {
				t.Errorf("SystemAccountID = %q; a collection that does not declare this field drops it silently", got.SystemAccountID)
			}
			latest := got.LatestSigningKey()
			if latest == nil {
				t.Fatal("LatestSigningKey() = nil; account JWT generation and the publisher both fail in this state")
			}
			if latest.PublicKey != "OSIGNINGPUB" || latest.Seed != "SOSIGNINGSEED" || latest.PrivateKey != "PSIGNINGPRIV" {
				t.Errorf("signing key = %+v, want OSIGNINGPUB/PSIGNINGPRIV/SOSIGNINGSEED", *latest)
			}
			if keys := got.AllSigningPublicKeys(); len(keys) != 1 || keys[0] != "OSIGNINGPUB" {
				t.Errorf("AllSigningPublicKeys() = %v", keys)
			}
			if got.Created.IsZero() || got.Updated.IsZero() {
				t.Error("Created/Updated not populated from autodate fields")
			}
		})
	}
}

// TestRecordToAccountModel_Boundary covers every field on an account, under both
// encryption settings, including the revocation list and all six limits.
func TestRecordToAccountModel_Boundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"plaintext", ""},
		{"encrypted", boundaryEncryptionKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, opts := newBoundaryFixture(t, tc.key)

			pub, priv := pbtypes.NewSigningKeyPair("AASIGNINGPUB", "PASIGNINGPRIV", "SAASIGNINGSEED")
			pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
				[]pbtypes.SigningKeyPublic{pub}, []pbtypes.SigningKeyPrivate{priv})
			if err != nil {
				t.Fatalf("marshal signing keys: %v", err)
			}

			record := core.NewRecord(mustCollection(t, app, opts.AccountCollectionName))
			record.Set("name", "Tenant A")
			record.Set("description", "boundary account")
			record.Set("public_key", "AACCOUNTPUB")
			record.Set("jwt", "eyJACCOUNT")
			record.Set("active", true)
			record.Set("revocations", `{"UREVOKEDPUB":1700000000}`)
			record.Set("signing_keys", pubJSON)
			record.Set("max_connections", -1)
			record.Set("max_subscriptions", 100)
			record.Set("max_data", 4096)
			record.Set("max_payload", 1024)
			record.Set("max_jetstream_disk_storage", 5000)
			record.Set("max_jetstream_memory_storage", 6000)
			if err := pbtypes.EncryptAndSet(record, "private_key", "PACCOUNTPRIV", tc.key); err != nil {
				t.Fatalf("encrypt private_key: %v", err)
			}
			if err := pbtypes.EncryptAndSet(record, "seed", "SAACCOUNTSEED", tc.key); err != nil {
				t.Fatalf("encrypt seed: %v", err)
			}
			if err := pbtypes.EncryptJSONAndSet(record, "signing_keys_private", privJSON, tc.key); err != nil {
				t.Fatalf("encrypt signing_keys_private: %v", err)
			}
			mustSave(t, app, record)

			got := pbtypes.RecordToAccountModel(
				reload(t, app, opts.AccountCollectionName, record.Id), tc.key)

			if got.ID != record.Id {
				t.Errorf("ID = %q, want %q", got.ID, record.Id)
			}
			if got.Name != "Tenant A" {
				t.Errorf("Name = %q", got.Name)
			}
			if got.Description != "boundary account" {
				t.Errorf("Description = %q", got.Description)
			}
			if got.PublicKey != "AACCOUNTPUB" {
				t.Errorf("PublicKey = %q", got.PublicKey)
			}
			if got.PrivateKey != "PACCOUNTPRIV" {
				t.Errorf("PrivateKey = %q, want decrypted", got.PrivateKey)
			}
			if got.Seed != "SAACCOUNTSEED" {
				t.Errorf("Seed = %q, want decrypted", got.Seed)
			}
			if got.JWT != "eyJACCOUNT" {
				t.Errorf("JWT = %q", got.JWT)
			}
			if !got.Active {
				t.Error("Active = false, want true")
			}

			// Limits: -1 (unlimited), 0 (disabled) and positive values are three
			// distinct meanings, so the sign has to survive the int64 conversion.
			for _, l := range []struct {
				name string
				got  int64
				want int64
			}{
				{"MaxConnections", got.MaxConnections, -1},
				{"MaxSubscriptions", got.MaxSubscriptions, 100},
				{"MaxData", got.MaxData, 4096},
				{"MaxPayload", got.MaxPayload, 1024},
				{"MaxJetStreamDiskStorage", got.MaxJetStreamDiskStorage, 5000},
				{"MaxJetStreamMemoryStorage", got.MaxJetStreamMemoryStorage, 6000},
			} {
				if l.got != l.want {
					t.Errorf("%s = %d, want %d", l.name, l.got, l.want)
				}
			}

			revs, err := got.GetRevocations()
			if err != nil {
				t.Fatalf("GetRevocations: %v", err)
			}
			if revs["UREVOKEDPUB"] != 1700000000 {
				t.Errorf("revocations = %v, want UREVOKEDPUB at 1700000000", revs)
			}

			latest := got.LatestSigningKey()
			if latest == nil {
				t.Fatal("LatestSigningKey() = nil; no user in this account could be issued a JWT")
			}
			if latest.PublicKey != "AASIGNINGPUB" || latest.Seed != "SAASIGNINGSEED" {
				t.Errorf("signing key = %+v", *latest)
			}
		})
	}
}

// TestRecordToUserModel_Boundary covers every field on a user, under both encryption
// settings. jwt_expires_at is the field a previous defect left unread.
func TestRecordToUserModel_Boundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"plaintext", ""},
		{"encrypted", boundaryEncryptionKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, opts := newBoundaryFixture(t, tc.key)
			accountID := seedAccount(t, app, opts, "Tenant A")
			roleID := seedRole(t, app, opts, "sensor")

			expires := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)

			record := core.NewRecord(mustCollection(t, app, opts.UserCollectionName))
			record.Set("nats_username", "device-001")
			record.Set("description", "boundary user")
			record.Set("public_key", "UUSERPUB")
			record.Set("account_id", accountID)
			record.Set("role_id", roleID)
			record.Set("jwt", "eyJUSER")
			record.Set("creds_file", "-----BEGIN NATS USER JWT-----")
			record.Set("bearer_token", true)
			record.Set("active", true)
			record.Set("regenerate", true)
			record.Set("jwt_expires_at", expires)
			record.Set("publish_permissions", `["tenant.a.>"]`)
			record.Set("subscribe_permissions", `["tenant.a.cmd.>"]`)
			record.Set("publish_deny_permissions", `["tenant.a.admin.>"]`)
			record.Set("subscribe_deny_permissions", `["$SYS.>"]`)
			record.Set("email", "device-001@example.com")
			record.Set("password", "boundary-test-password")
			if err := pbtypes.EncryptAndSet(record, "private_key", "PUSERPRIV", tc.key); err != nil {
				t.Fatalf("encrypt private_key: %v", err)
			}
			if err := pbtypes.EncryptAndSet(record, "seed", "SUUSERSEED", tc.key); err != nil {
				t.Fatalf("encrypt seed: %v", err)
			}
			mustSave(t, app, record)

			got := pbtypes.RecordToUserModel(
				reload(t, app, opts.UserCollectionName, record.Id), tc.key)

			if got.ID != record.Id {
				t.Errorf("ID = %q, want %q", got.ID, record.Id)
			}
			if got.NatsUsername != "device-001" {
				t.Errorf("NatsUsername = %q", got.NatsUsername)
			}
			if got.Description != "boundary user" {
				t.Errorf("Description = %q", got.Description)
			}
			if got.PublicKey != "UUSERPUB" {
				t.Errorf("PublicKey = %q", got.PublicKey)
			}
			if got.PrivateKey != "PUSERPRIV" {
				t.Errorf("PrivateKey = %q, want decrypted", got.PrivateKey)
			}
			if got.Seed != "SUUSERSEED" {
				t.Errorf("Seed = %q, want decrypted", got.Seed)
			}
			if got.AccountID != accountID {
				t.Errorf("AccountID = %q, want %q", got.AccountID, accountID)
			}
			if got.RoleID != roleID {
				t.Errorf("RoleID = %q, want %q", got.RoleID, roleID)
			}
			if got.JWT != "eyJUSER" {
				t.Errorf("JWT = %q", got.JWT)
			}
			if got.CredsFile != "-----BEGIN NATS USER JWT-----" {
				t.Errorf("CredsFile = %q", got.CredsFile)
			}
			if !got.BearerToken {
				t.Error("BearerToken = false, want true")
			}
			if !got.Active {
				t.Error("Active = false, want true")
			}
			if !got.Regenerate {
				t.Error("Regenerate = false, want true")
			}

			// A nil pointer means "no expiry"; a zero time would mean "expired at the
			// epoch". The generator branches on the pointer, so the distinction matters.
			if got.JWTExpiresAt == nil {
				t.Error("JWTExpiresAt = nil; the per-user expiry never reaches the generator in this state")
			} else if !got.JWTExpiresAt.Equal(expires) {
				t.Errorf("JWTExpiresAt = %v, want %v", *got.JWTExpiresAt, expires)
			}

			assertJSONEqual(t, "PublishPermissions", got.PublishPermissions, `["tenant.a.>"]`)
			assertJSONEqual(t, "SubscribePermissions", got.SubscribePermissions, `["tenant.a.cmd.>"]`)
			assertJSONEqual(t, "PublishDenyPermissions", got.PublishDenyPermissions, `["tenant.a.admin.>"]`)
			assertJSONEqual(t, "SubscribeDenyPermissions", got.SubscribeDenyPermissions, `["$SYS.>"]`)
		})
	}
}

// TestRecordToRoleModel_Boundary covers every field on a role. Roles carry the baseline
// permissions every user in them inherits, so a field that fails to cross this boundary
// silently widens or narrows what an entire class of clients may do.
func TestRecordToRoleModel_Boundary(t *testing.T) {
	app, opts := newBoundaryFixture(t, "")

	record := core.NewRecord(mustCollection(t, app, opts.RoleCollectionName))
	record.Set("name", "sensor")
	record.Set("description", "boundary role")
	record.Set("is_default", true)
	record.Set("publish_permissions", `["sensor.>","telemetry.*"]`)
	record.Set("subscribe_permissions", `["cmd.>"]`)
	record.Set("publish_deny_permissions", `["sensor.admin.>"]`)
	record.Set("subscribe_deny_permissions", `["$SYS.>"]`)
	record.Set("allow_response", true)
	record.Set("allow_response_max", 5)
	record.Set("allow_response_ttl", 60)
	record.Set("max_subscriptions", 50)
	record.Set("max_data", 8192)
	record.Set("max_payload", 2048)
	mustSave(t, app, record)

	got := pbtypes.RecordToRoleModel(reload(t, app, opts.RoleCollectionName, record.Id))

	if got.ID != record.Id {
		t.Errorf("ID = %q, want %q", got.ID, record.Id)
	}
	if got.Name != "sensor" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "boundary role" {
		t.Errorf("Description = %q", got.Description)
	}
	if !got.IsDefault {
		t.Error("IsDefault = false, want true")
	}
	if !got.AllowResponse {
		t.Error("AllowResponse = false, want true")
	}
	if got.AllowResponseMax != 5 {
		t.Errorf("AllowResponseMax = %d, want 5", got.AllowResponseMax)
	}
	if got.AllowResponseTTL != 60 {
		t.Errorf("AllowResponseTTL = %d, want 60", got.AllowResponseTTL)
	}
	if got.MaxSubscriptions != 50 {
		t.Errorf("MaxSubscriptions = %d, want 50", got.MaxSubscriptions)
	}
	if got.MaxData != 8192 {
		t.Errorf("MaxData = %d, want 8192", got.MaxData)
	}
	if got.MaxPayload != 2048 {
		t.Errorf("MaxPayload = %d, want 2048", got.MaxPayload)
	}

	assertJSONEqual(t, "PublishPermissions", got.PublishPermissions, `["sensor.>","telemetry.*"]`)
	assertJSONEqual(t, "SubscribePermissions", got.SubscribePermissions, `["cmd.>"]`)
	assertJSONEqual(t, "PublishDenyPermissions", got.PublishDenyPermissions, `["sensor.admin.>"]`)
	assertJSONEqual(t, "SubscribeDenyPermissions", got.SubscribeDenyPermissions, `["$SYS.>"]`)
}

// TestRecordToExportModel_Boundary covers every field on an account export. Exports are
// embedded in the account JWT, so a field lost here silently changes what other accounts
// are allowed to import.
func TestRecordToExportModel_Boundary(t *testing.T) {
	app, opts := newBoundaryFixture(t, "")
	accountID := seedAccount(t, app, opts, "Exporter")

	record := core.NewRecord(mustCollection(t, app, opts.ExportCollectionName))
	record.Set("account_id", accountID)
	record.Set("name", "weather-service")
	record.Set("subject", "weather.*.forecast")
	record.Set("type", "service")
	record.Set("token_req", true)
	record.Set("response_type", "Chunked")
	record.Set("response_threshold", 250)
	record.Set("account_token_position", 2)
	record.Set("advertise", true)
	record.Set("allow_trace", true)
	record.Set("description", "boundary export")
	mustSave(t, app, record)

	got := pbtypes.RecordToExportModel(reload(t, app, opts.ExportCollectionName, record.Id))

	if got.ID != record.Id {
		t.Errorf("ID = %q, want %q", got.ID, record.Id)
	}
	if got.AccountID != accountID {
		t.Errorf("AccountID = %q, want %q", got.AccountID, accountID)
	}
	if got.Name != "weather-service" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Subject != "weather.*.forecast" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Type != "service" {
		t.Errorf("Type = %q, want service", got.Type)
	}
	if !got.TokenReq {
		t.Error("TokenReq = false, want true; a token-required export becomes public without it")
	}
	if got.ResponseType != "Chunked" {
		t.Errorf("ResponseType = %q, want Chunked", got.ResponseType)
	}
	if got.ResponseThreshold != 250 {
		t.Errorf("ResponseThreshold = %d, want 250", got.ResponseThreshold)
	}
	if got.AccountTokenPosition != 2 {
		t.Errorf("AccountTokenPosition = %d, want 2", got.AccountTokenPosition)
	}
	if !got.Advertise {
		t.Error("Advertise = false, want true")
	}
	if !got.AllowTrace {
		t.Error("AllowTrace = false, want true")
	}
	if got.Description != "boundary export" {
		t.Errorf("Description = %q", got.Description)
	}
}

// TestRecordToImportModel_Boundary covers every field on an account import. `token` is
// the activation JWT for a token-required export, and `local_subject` remaps the imported
// subject — both silently break cross-account traffic if they fail to cross.
func TestRecordToImportModel_Boundary(t *testing.T) {
	app, opts := newBoundaryFixture(t, "")
	accountID := seedAccount(t, app, opts, "Importer")

	record := core.NewRecord(mustCollection(t, app, opts.ImportCollectionName))
	record.Set("account_id", accountID)
	record.Set("name", "weather-import")
	record.Set("subject", "weather.*.forecast")
	record.Set("account", "AEXPORTINGACCOUNTPUBLICKEY")
	record.Set("token", "eyJACTIVATIONTOKEN")
	record.Set("local_subject", "external.weather.forecast")
	record.Set("type", "service")
	record.Set("share", true)
	record.Set("allow_trace", true)
	record.Set("description", "boundary import")
	mustSave(t, app, record)

	got := pbtypes.RecordToImportModel(reload(t, app, opts.ImportCollectionName, record.Id))

	if got.ID != record.Id {
		t.Errorf("ID = %q, want %q", got.ID, record.Id)
	}
	if got.AccountID != accountID {
		t.Errorf("AccountID = %q, want %q", got.AccountID, accountID)
	}
	if got.Name != "weather-import" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Subject != "weather.*.forecast" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Account != "AEXPORTINGACCOUNTPUBLICKEY" {
		t.Errorf("Account = %q", got.Account)
	}
	if got.Token != "eyJACTIVATIONTOKEN" {
		t.Errorf("Token = %q; without it a token-required import cannot activate", got.Token)
	}
	if got.LocalSubject != "external.weather.forecast" {
		t.Errorf("LocalSubject = %q", got.LocalSubject)
	}
	if got.Type != "service" {
		t.Errorf("Type = %q, want service", got.Type)
	}
	if !got.Share {
		t.Error("Share = false, want true")
	}
	if !got.AllowTrace {
		t.Error("AllowTrace = false, want true")
	}
	if got.Description != "boundary import" {
		t.Errorf("Description = %q", got.Description)
	}
}
