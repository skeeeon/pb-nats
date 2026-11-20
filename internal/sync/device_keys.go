package sync

import (
	"fmt"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"github.com/nats-io/nkeys"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// DEVICE-HELD KEYS
//
// A nats_users row with device_key = true holds only the public half of its key
// pair. The device made the pair and sent the public key; pb-nats signs a JWT
// for it and never stores a seed, a private key or a creds_file. An empty
// public_key means "waiting for the device".
//
// The invariants live here, in the library, because the library breaks without
// them, whoever saved the row and however (REST or app.Save):
//
//  1. device_key is set at create and never changes.
//  2. public_key changes only from empty, and only on a device_key row. A
//     platform-held key changes only through revoke, which rotates the pair.
//  3. An enrolled key is a valid user nkey, and not revoked in the account.
//
// Plus two that keep the claim honest: a device_key row is never a bearer token
// (a bearer JWT never signs the server's nonce, so the key would protect
// nothing), and never carries a seed or private key.
//
// Uniqueness is the partial unique index on public_key (collections).
//
// generateUserJWT is the one place that decides whether a device_key row gets a
// JWT: see the guard at its top.

// saveInternal saves a user record as a pb-nats-initiated write, so the update
// hook skips the public_key check for it. That check refuses a public_key
// change on a platform-held key, which is exactly what the create hook's own
// save makes after generating the pair.
func (sm *Manager) saveInternal(record *core.Record) error {
	sm.internalSaves.Store(record.Id, struct{}{})
	defer sm.internalSaves.Delete(record.Id)
	return sm.app.Save(record)
}

// isInternalSave reports whether the record is being saved by saveInternal.
func (sm *Manager) isInternalSave(record *core.Record) bool {
	_, ok := sm.internalSaves.Load(record.Id)
	return ok
}

// refuse turns an invariant error into a 400 that still matches errors.Is:
// router.ApiError.Is delegates to its raw data. A plain error from a hook would
// reach a REST caller as the generic "Failed to update record."
func refuse(err error) error {
	return router.NewBadRequestError(err.Error(), err)
}

// checkUserCreate enforces the device-key invariants on a new nats_users row,
// before it is inserted.
func (sm *Manager) checkUserCreate(app core.App, record *core.Record) error {
	pub := record.GetString("public_key")

	if !record.GetBool("device_key") {
		// A platform-held key's public_key always comes with its seed. pb-nats'
		// own system user arrives this way; a public_key alone made
		// generateUserKeys skip generation and left a row with no JWT, ever.
		if pub != "" && record.GetString("seed") == "" {
			return refuse(pbtypes.ErrPublicKeyWithoutSeed)
		}
		return nil
	}

	if err := checkDeviceRowShape(record); err != nil {
		return err
	}
	if pub != "" {
		return sm.checkEnrollment(app, record, pub)
	}
	return nil
}

// checkUserUpdate enforces the device-key invariants on a nats_users save. It
// runs first in the update hook, before revoke rotates or empties public_key,
// so it compares what the caller wrote against the stored row.
func (sm *Manager) checkUserUpdate(app core.App, record *core.Record) error {
	// pb-nats' own saves are trusted, and must be: the create hook saves a
	// record whose Original() is still the empty pre-insert state, so every
	// field there would look changed.
	if sm.isInternalSave(record) {
		return nil
	}

	original, err := storedUser(app, record)
	if err != nil {
		return err
	}
	deviceKey := record.GetBool("device_key")

	if deviceKey != original.GetBool("device_key") {
		return refuse(pbtypes.ErrDeviceKeyFrozen)
	}
	if deviceKey {
		if err := checkDeviceRowShape(record); err != nil {
			return err
		}
	}

	pub := record.GetString("public_key")
	if pub == original.GetString("public_key") {
		return nil
	}
	if !deviceKey {
		return refuse(pbtypes.ErrPublicKeyImmutable)
	}
	if original.GetString("public_key") != "" {
		return refuse(pbtypes.ErrDeviceKeyEnrolled)
	}
	return sm.checkEnrollment(app, record, pub)
}

// storedUser is the row as it stands before this save. Original() is that row
// whenever the record was read from the database. But a record created and then
// saved again from the same object (a consumer creating a row and filling it in)
// keeps the blank pre-insert snapshot, where every field looks changed. So a
// blank snapshot is replaced by a read, through the app the save runs in, so a
// row created earlier in the same transaction is found.
func storedUser(app core.App, record *core.Record) (*core.Record, error) {
	original := record.Original()
	if original.Id != "" {
		return original, nil
	}
	stored, err := app.FindRecordById(record.Collection(), record.Id)
	if err != nil {
		return nil, fmt.Errorf("read user %s before checking its keys: %w", record.Id, err)
	}
	return stored, nil
}

// checkDeviceRowShape refuses the two field combinations a device_key row can
// never have, on create and on every update: bearer_token can be switched on
// long after the key arrives.
func checkDeviceRowShape(record *core.Record) error {
	if record.GetBool("bearer_token") {
		return refuse(pbtypes.ErrDeviceKeyBearer)
	}
	if record.GetString("seed") != "" || record.GetString("private_key") != "" {
		return refuse(pbtypes.ErrDeviceKeyHasSecret)
	}
	return nil
}

// checkEnrollment validates a public key arriving on a device_key row.
func (sm *Manager) checkEnrollment(app core.App, record *core.Record, pub string) error {
	if !nkeys.IsValidPublicUserKey(pub) {
		return refuse(pbtypes.ErrInvalidDeviceKey)
	}

	// A revocation is a cutoff time, not a ban: a revoked key sent again would
	// get a JWT issued after the cutoff, which NATS accepts.
	accountID := record.GetString("account_id")
	if accountID == "" {
		return nil // the required relation fails the save on its own
	}
	account, err := app.FindRecordById(sm.options.AccountCollectionName, accountID)
	if err != nil {
		return fmt.Errorf("find account %s to check revocations: %w", accountID, err)
	}
	revocations, err := pbtypes.RecordToAccountModel(account, sm.options.EncryptionKey).GetRevocations()
	if err != nil {
		return fmt.Errorf("read revocations of account %s: %w", accountID, err)
	}
	if _, revoked := revocations[pub]; revoked {
		return refuse(pbtypes.ErrDeviceKeyRevoked)
	}
	return nil
}

// revokeDeviceKey is revoke on a device_key row. The leaked material is the
// device's own key, which pb-nats cannot replace, so it revokes the key and
// empties public_key and jwt: the device enrolls a new key before it
// reconnects. With no key yet there is nothing to revoke.
func (sm *Manager) revokeDeviceKey(record *core.Record) error {
	pub := record.GetString("public_key")
	if pub == "" {
		return nil
	}
	if err := sm.revokeAccountUserKey(record.GetString("account_id"), pub, time.Now()); err != nil {
		return err
	}
	record.Set("public_key", "")
	record.Set("jwt", "")
	record.Set("creds_file", "")
	return nil
}
