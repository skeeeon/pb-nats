package types

import "errors"

// Device-held key errors.
//
// They live here, not in the root package, so internal/sync can wrap them
// without an import cycle (the root package imports internal/sync). The root
// package re-exports each one so a consumer can match with errors.Is: a route
// that enrolls a device's key tells "already enrolled" from "not a key" this
// way, never by matching the message.
var (
	// ErrDeviceKeyFrozen: device_key is set when the row is created and never
	// changes. Flipping it either leaves a row with no seed to build credentials
	// from, or keeps a seed while claiming the platform never saw it.
	ErrDeviceKeyFrozen = errors.New("device_key is set at create and cannot change")

	// ErrDeviceKeyBearer: a bearer JWT never signs the server's nonce, so a key
	// the device holds would protect nothing.
	ErrDeviceKeyBearer = errors.New("a device-held key cannot be a bearer token")

	// ErrDeviceKeyHasSecret: the platform never holds the private half of a
	// device-held key, so a device_key row carries no seed or private key.
	ErrDeviceKeyHasSecret = errors.New("a device-held key row cannot carry a seed or private key")

	// ErrDeviceKeyEnrolled: a device's public key changes only from empty.
	// Only revoke empties it again.
	ErrDeviceKeyEnrolled = errors.New("a key is already enrolled for this user")

	// ErrInvalidDeviceKey: not a NATS user public key (nkey, starts with U).
	ErrInvalidDeviceKey = errors.New("not a valid NATS user public key")

	// ErrDeviceKeyRevoked: the key is on the account's revocation list. A
	// revocation is a cutoff time, so the key sent again would get a fresh JWT
	// that passes it.
	ErrDeviceKeyRevoked = errors.New("this key has been revoked; generate a new one")

	// ErrPublicKeyImmutable: a platform-held key changes only through revoke,
	// which rotates the whole pair. Overwriting public_key by hand left revoke
	// revoking the wrong key.
	ErrPublicKeyImmutable = errors.New("public_key on a platform-held key changes only through revoke")

	// ErrPublicKeyWithoutSeed: a platform-held key's public_key always comes
	// with its seed. A row created with one and not the other got no JWT, ever.
	ErrPublicKeyWithoutSeed = errors.New("a platform-held public_key needs its seed; use device_key for a key the device holds")
)
