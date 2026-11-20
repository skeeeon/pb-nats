package sync

import (
	"errors"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// newDeviceKeyFixture is newSyncFixture with the user hooks bound, and the
// fields those hooks read. These tests drive real saves through the hooks:
// every bug the device-key guard fixes lived in a path (the empty-jwt reissue,
// the active edge, the bulk loops) that a direct call to generateUserJWT skips.
func newDeviceKeyFixture(t *testing.T) *syncFixture {
	t.Helper()
	f := newSyncFixture(t)

	users, err := f.app.FindCollectionByNameOrId(f.options.UserCollectionName)
	if err != nil {
		t.Fatalf("find users collection: %v", err)
	}
	users.Fields.Add(&core.BoolField{Name: "device_key"})
	users.Fields.Add(&core.BoolField{Name: "bearer_token"})
	users.Fields.Add(&core.BoolField{Name: "revoke"})
	users.Fields.Add(&core.BoolField{Name: "regenerate"})
	if err := f.app.Save(users); err != nil {
		t.Fatalf("add hook fields to users collection: %v", err)
	}

	// revoke regenerates the account JWT to embed the revocation, and that is
	// signed by the operator.
	nm := nkey.NewManager()
	_, opPub, opSigningSeed, opSigningPub, err := nm.GenerateOperatorKeyPair()
	if err != nil {
		t.Fatalf("GenerateOperatorKeyPair: %v", err)
	}
	signPub, signPriv := pbtypes.NewSigningKeyPair(opSigningPub, "", opSigningSeed)
	pubJSON, privJSON, err := pbtypes.MarshalSigningKeys(
		[]pbtypes.SigningKeyPublic{signPub},
		[]pbtypes.SigningKeyPrivate{signPriv},
	)
	if err != nil {
		t.Fatalf("MarshalSigningKeys: %v", err)
	}
	operators := core.NewBaseCollection(pbtypes.SystemOperatorCollectionName)
	operators.Fields.Add(&core.TextField{Name: "name", Max: 100})
	operators.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	operators.Fields.Add(&core.JSONField{Name: "signing_keys", MaxSize: 10000})
	operators.Fields.Add(&core.JSONField{Name: "signing_keys_private", MaxSize: 10000})
	if err := f.app.Save(operators); err != nil {
		t.Fatalf("save operator collection: %v", err)
	}
	operator := core.NewRecord(operators)
	operator.Set("name", "op")
	operator.Set("public_key", opPub)
	operator.Set("signing_keys", pubJSON)
	operator.Set("signing_keys_private", privJSON)
	if err := f.app.Save(operator); err != nil {
		t.Fatalf("save operator: %v", err)
	}

	f.sm.setupUserHooks()
	return f
}

// newUserKey returns a fresh user public key, as `nk -gen user -pubout` prints.
func newUserKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("user public key: %v", err)
	}
	return pub
}

// create saves a new user through the hooks. pub may be empty.
func (f *syncFixture) create(t *testing.T, deviceKey, active bool, pub string) (*core.Record, error) {
	t.Helper()
	users, err := f.app.FindCollectionByNameOrId(f.options.UserCollectionName)
	if err != nil {
		t.Fatalf("find users collection: %v", err)
	}
	r := core.NewRecord(users)
	r.Set("nats_username", "gateway")
	r.Set("account_id", f.account.Id)
	r.Set("role_id", f.role.Id)
	r.Set("active", active)
	r.Set("device_key", deviceKey)
	r.Set("public_key", pub)
	return r, f.app.Save(r)
}

func (f *syncFixture) mustCreate(t *testing.T, deviceKey, active bool, pub string) *core.Record {
	t.Helper()
	r, err := f.create(t, deviceKey, active, pub)
	if err != nil {
		t.Fatalf("create user (device_key=%v, active=%v): %v", deviceKey, active, err)
	}
	return f.reload(t, r.Id)
}

// update reloads the user, applies set, and saves through the hooks.
func (f *syncFixture) update(t *testing.T, id string, set map[string]any) (*core.Record, error) {
	t.Helper()
	r := f.reload(t, id)
	for k, v := range set {
		r.Set(k, v)
	}
	if err := f.app.Save(r); err != nil {
		return nil, err
	}
	return f.reload(t, id), nil
}

func (f *syncFixture) mustUpdate(t *testing.T, id string, set map[string]any) *core.Record {
	t.Helper()
	r, err := f.update(t, id, set)
	if err != nil {
		t.Fatalf("update %v: %v", set, err)
	}
	return r
}

// jwtSubject decodes a user JWT and returns the key it was signed for.
func jwtSubject(t *testing.T, token string) string {
	t.Helper()
	claims, err := natsjwt.DecodeUserClaims(token)
	if err != nil {
		t.Fatalf("decode user JWT: %v", err)
	}
	return claims.Subject
}

func wantRefused(t *testing.T, err, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("save succeeded, want it refused with %q", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("save refused with %v, want errors.Is(%q)", err, target)
	}
}

// A device_key row with no key yet holds nothing, and every save on it works.
// Before the guard, the update hook reissued whenever jwt was empty, tried to
// sign an empty key and failed the save, so a waiting row could not be edited,
// deactivated or reactivated.
func TestDeviceKeyWaitingRowSaves(t *testing.T) {
	f := newDeviceKeyFixture(t)

	r := f.mustCreate(t, true, true, "")
	for _, field := range []string{"public_key", "seed", "private_key", "jwt", "creds_file"} {
		if got := r.GetString(field); got != "" {
			t.Errorf("waiting row %s = %q, want empty", field, got)
		}
	}

	r = f.mustUpdate(t, r.Id, map[string]any{"nats_username": "renamed"})
	r = f.mustUpdate(t, r.Id, map[string]any{"active": false})
	r = f.mustUpdate(t, r.Id, map[string]any{"active": true})
	r = f.mustUpdate(t, r.Id, map[string]any{"regenerate": true})
	if r.GetString("jwt") != "" {
		t.Error("a waiting row got a JWT with no key to sign")
	}
}

// A key arriving signs a JWT for exactly that key, with no creds_file.
func TestDeviceKeyArrivalSignsJWT(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, "")

	pub := newUserKey(t)
	r = f.mustUpdate(t, r.Id, map[string]any{"public_key": pub})

	token := r.GetString("jwt")
	if token == "" {
		t.Fatal("no JWT after the key arrived")
	}
	if got := jwtSubject(t, token); got != pub {
		t.Errorf("JWT signed for %s, want the enrolled key %s", got, pub)
	}
	if r.GetString("creds_file") != "" {
		t.Error("a device-key row got a creds_file; only the device can build one")
	}
	if r.GetString("seed") != "" || r.GetString("private_key") != "" {
		t.Error("a device-key row holds a seed or private key")
	}
}

// A create may carry the key (a key generated at manufacture, a stone apply).
func TestDeviceKeyCreateWithKey(t *testing.T) {
	f := newDeviceKeyFixture(t)
	pub := newUserKey(t)

	r := f.mustCreate(t, true, true, pub)
	if got := jwtSubject(t, r.GetString("jwt")); got != pub {
		t.Errorf("JWT signed for %s, want %s", got, pub)
	}
	if r.GetString("creds_file") != "" {
		t.Error("a device-key row got a creds_file")
	}
}

// Every bulk reissue reaches an enrolled device-key row. Before the guard,
// generateUserJWT always built a creds_file, which needs the seed, so a role
// edit or a signing-key change silently left the device on its old JWT.
func TestDeviceKeyBulkReissue(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, newUserKey(t))

	for name, run := range map[string]func() error{
		"regenerateUsersWithRole":  func() error { return f.sm.regenerateUsersWithRole(f.role.Id) },
		"regenerateUsersInAccount": func() error { return f.sm.regenerateUsersInAccount(f.account.Id) },
	} {
		r = f.mustUpdate(t, r.Id, map[string]any{"jwt": "sentinel-jwt"})
		if err := run(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r = f.reload(t, r.Id)
		if got := r.GetString("jwt"); got == "sentinel-jwt" || got == "" {
			t.Errorf("%s did not reissue the device-key row (jwt = %q)", name, got)
		}
		if r.GetString("creds_file") != "" {
			t.Errorf("%s wrote a creds_file for a device-key row", name)
		}
	}
}

// A key arriving on an inactive row mints nothing; reactivation signs it. Before
// the guard, hasPriorState (keyed on the original public_key) skipped the active
// edge for a waiting row, and the key arriving minted a working JWT for a row
// that was meant to be off.
func TestDeviceKeyOnInactiveRowMintsNothing(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, false, "")

	pub := newUserKey(t)
	r = f.mustUpdate(t, r.Id, map[string]any{"public_key": pub})
	if r.GetString("jwt") != "" {
		t.Fatal("a key arriving on an inactive row minted a JWT")
	}

	r = f.mustUpdate(t, r.Id, map[string]any{"active": true})
	if got := jwtSubject(t, r.GetString("jwt")); got != pub {
		t.Errorf("after reactivation JWT signed for %s, want %s", got, pub)
	}
}

// public_key changes only from empty. Pair the cannot with the can: the same
// row took its first key above.
func TestDeviceKeyWriteOnce(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, newUserKey(t))

	_, err := f.update(t, r.Id, map[string]any{"public_key": newUserKey(t)})
	wantRefused(t, err, pbtypes.ErrDeviceKeyEnrolled)

	_, err = f.update(t, r.Id, map[string]any{"public_key": ""})
	wantRefused(t, err, pbtypes.ErrDeviceKeyEnrolled)
}

// device_key is frozen at create, in both directions.
func TestDeviceKeyFrozen(t *testing.T) {
	f := newDeviceKeyFixture(t)

	device := f.mustCreate(t, true, true, "")
	_, err := f.update(t, device.Id, map[string]any{"device_key": false})
	wantRefused(t, err, pbtypes.ErrDeviceKeyFrozen)

	platform := f.mustCreate(t, false, true, "")
	_, err = f.update(t, platform.Id, map[string]any{"device_key": true})
	wantRefused(t, err, pbtypes.ErrDeviceKeyFrozen)
}

// Never device_key and bearer_token together, on create and on every update.
func TestDeviceKeyBearer(t *testing.T) {
	f := newDeviceKeyFixture(t)

	users, _ := f.app.FindCollectionByNameOrId(f.options.UserCollectionName)
	r := core.NewRecord(users)
	r.Set("nats_username", "gateway")
	r.Set("account_id", f.account.Id)
	r.Set("role_id", f.role.Id)
	r.Set("device_key", true)
	r.Set("bearer_token", true)
	wantRefused(t, f.app.Save(r), pbtypes.ErrDeviceKeyBearer)

	enrolled := f.mustCreate(t, true, true, newUserKey(t))
	_, err := f.update(t, enrolled.Id, map[string]any{"bearer_token": true})
	wantRefused(t, err, pbtypes.ErrDeviceKeyBearer)
}

// A device_key row never carries a seed.
func TestDeviceKeyHasNoSecret(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, "")

	_, err := f.update(t, r.Id, map[string]any{"seed": "SUAFAKE"})
	wantRefused(t, err, pbtypes.ErrDeviceKeyHasSecret)
}

// Only a user nkey enrolls.
func TestDeviceKeyInvalid(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, "")

	_, err := f.update(t, r.Id, map[string]any{"public_key": "not-a-key"})
	wantRefused(t, err, pbtypes.ErrInvalidDeviceKey)

	// An account key is a valid nkey, but not a user one.
	_, err = f.update(t, r.Id, map[string]any{"public_key": f.account.GetString("public_key")})
	wantRefused(t, err, pbtypes.ErrInvalidDeviceKey)
}

// revoke on a device-key row revokes the key and empties the row; the device
// enrolls a new key. The revoked key cannot come back: a revocation is a cutoff,
// so it would get a JWT that passes it.
func TestDeviceKeyRevoke(t *testing.T) {
	f := newDeviceKeyFixture(t)
	old := newUserKey(t)
	r := f.mustCreate(t, true, true, old)

	r = f.mustUpdate(t, r.Id, map[string]any{"revoke": true})
	if r.GetString("public_key") != "" || r.GetString("jwt") != "" {
		t.Fatalf("after revoke public_key = %q, jwt = %q, want both empty",
			r.GetString("public_key"), r.GetString("jwt"))
	}
	if r.GetBool("revoke") {
		t.Error("revoke flag not cleared")
	}

	account, err := f.app.FindRecordById(f.options.AccountCollectionName, f.account.Id)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	revocations, err := pbtypes.RecordToAccountModel(account).GetRevocations()
	if err != nil {
		t.Fatalf("read revocations: %v", err)
	}
	if _, ok := revocations[old]; !ok {
		t.Error("the old device key is not on the account's revocation list")
	}

	_, err = f.update(t, r.Id, map[string]any{"public_key": old})
	wantRefused(t, err, pbtypes.ErrDeviceKeyRevoked)

	fresh := newUserKey(t)
	r = f.mustUpdate(t, r.Id, map[string]any{"public_key": fresh})
	if got := jwtSubject(t, r.GetString("jwt")); got != fresh {
		t.Errorf("JWT signed for %s, want the newly enrolled %s", got, fresh)
	}
}

// revoke on a row with no key yet has nothing to revoke, and is not an error.
func TestDeviceKeyRevokeWaitingRow(t *testing.T) {
	f := newDeviceKeyFixture(t)
	r := f.mustCreate(t, true, true, "")

	r = f.mustUpdate(t, r.Id, map[string]any{"revoke": true})
	if r.GetBool("revoke") {
		t.Error("revoke flag not cleared")
	}
}

// A platform-held key: the create hook's own save sets public_key (allowed, it
// is an internal save), a hand-written one is refused, and revoke still rotates.
// Before, an owner could overwrite public_key and revoke then revoked the wrong
// key.
func TestPlatformKeyPublicKeyImmutable(t *testing.T) {
	f := newDeviceKeyFixture(t)

	r := f.mustCreate(t, false, true, "")
	generated := r.GetString("public_key")
	if generated == "" || r.GetString("creds_file") == "" {
		t.Fatal("platform-key row got no generated key or creds_file at create")
	}

	_, err := f.update(t, r.Id, map[string]any{"public_key": newUserKey(t)})
	wantRefused(t, err, pbtypes.ErrPublicKeyImmutable)

	r = f.mustUpdate(t, r.Id, map[string]any{"revoke": true})
	if got := r.GetString("public_key"); got == generated || got == "" {
		t.Errorf("revoke did not rotate the platform-held key (public_key = %q)", got)
	}
}

// A platform-held public_key always comes with its seed. A public_key alone made
// key generation skip and left a row with no JWT, ever.
func TestPlatformKeyCreateWithoutSeed(t *testing.T) {
	f := newDeviceKeyFixture(t)

	_, err := f.create(t, false, true, newUserKey(t))
	wantRefused(t, err, pbtypes.ErrPublicKeyWithoutSeed)
}

// A consumer that creates a row and saves the same object again (filling in the
// key) must not be refused. That object's Original() is still the blank
// pre-insert snapshot, where device_key looks like it flipped; storedUser reads
// the row instead.
func TestDeviceKeyCreateThenSaveSameObject(t *testing.T) {
	f := newDeviceKeyFixture(t)

	r, err := f.create(t, true, true, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pub := newUserKey(t)
	r.Set("public_key", pub)
	if err := f.app.Save(r); err != nil {
		t.Fatalf("second save of the created object refused: %v", err)
	}
	if got := jwtSubject(t, f.reload(t, r.Id).GetString("jwt")); got != pub {
		t.Errorf("JWT signed for %s, want %s", got, pub)
	}
}
