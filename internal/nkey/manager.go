// Package nkey provides NKey management for NATS JWT authentication
package nkey

import (
	"github.com/nats-io/nkeys"
)

// Manager handles NKey generation and management for NATS authentication.
// This component provides a simplified interface to the NATS nkeys library
// with consistent error handling and key format management.
//
// KEY STORAGE PHILOSOPHY:
// Keys are stored as plaintext in the database by design for simplicity.
// This follows the architecture principle of keeping the system focused
// and avoiding complex encryption key management that would complicate
// the bootstrap process and operational procedures.
//
// NATS KEY HIERARCHY:
// - Operator Keys: Root of trust, signs account JWTs
// - Account Keys: Tenant boundaries, identity + signing keys  
// - User Keys: Individual authentication within accounts
//
// KEY FORMATS:
// All methods return keys in standard NATS formats:
// - Seeds: Private key material (SUAC..., SAAC..., SUUC...)
// - Public Keys: Public identifiers (OABC..., AABC..., UABC...)
// - Private Keys: Full private key encoding
type Manager struct{}

// NewManager creates a new NKey manager instance.
// The manager is stateless and can be safely used concurrently.
//
// RETURNS:
// - Manager instance ready for key operations
//
// THREAD SAFETY:
// All manager methods are thread-safe as they only use stateless
// operations from the underlying nkeys library.
func NewManager() *Manager {
	return &Manager{}
}

// GenerateOperatorKeyPair generates a complete key set for a NATS operator.
// Operators are the root of trust in the NATS JWT hierarchy and require
// both identity keys and signing keys.
//
// OPERATOR KEY STRUCTURE:
// - Main Key Pair: Operator identity and root signing capability
// - Signing Key Pair: Dedicated keys for signing account JWTs
//
// KEY SEPARATION BENEFITS:
// - Main keys identify the operator
// - Signing keys can be rotated without changing operator identity
// - Follows NATS security best practices
//
// RETURNS:
// - seed: Main operator private key seed (SUOC...)
// - public: Main operator public key (OABC...)  
// - signingKey: Signing private key seed (SUOC...)
// - signingPublic: Signing public key (OABC...)
// - error: nil on success, error on key generation failure
//
// KEY FORMATS:
// All returned keys are in standard NATS string format ready for
// database storage and JWT operations.
//
// SIDE EFFECTS: None (pure key generation)
func (m *Manager) GenerateOperatorKeyPair() (seed, public, signingKey, signingPublic string, err error) {
	// Create main operator key pair
	operatorKP, err := nkeys.CreateOperator()
	if err != nil {
		return "", "", "", "", err
	}

	public, err = operatorKP.PublicKey()
	if err != nil {
		return "", "", "", "", err
	}

	seedBytes, err := operatorKP.Seed()
	if err != nil {
		return "", "", "", "", err
	}
	seed = string(seedBytes)

	// Create signing key pair for the operator
	signingKP, err := nkeys.CreatePair(nkeys.PrefixByteOperator)
	if err != nil {
		return "", "", "", "", err
	}

	signingPublic, err = signingKP.PublicKey()
	if err != nil {
		return "", "", "", "", err
	}

	signingKeyBytes, err := signingKP.Seed()
	if err != nil {
		return "", "", "", "", err
	}
	signingKey = string(signingKeyBytes)

	return seed, public, signingKey, signingPublic, nil
}

// GenerateAccountKeyPair generates a complete key set for a NATS account.
// Accounts provide tenant isolation boundaries and require both identity
// keys and signing keys for user JWT creation.
//
// ACCOUNT KEY STRUCTURE:
// - Main Key Pair: Account identity and primary authentication
// - Signing Key Pair: Used to sign user JWTs within this account
//
// TENANT ISOLATION:
// Each account gets its own signing keys, ensuring users in different
// accounts cannot forge JWTs for other accounts even if keys are compromised.
//
// SIGNING KEY ROTATION:
// Account signing keys can be rotated independently of the account identity,
// immediately invalidating all user JWTs (they must be regenerated with new key).
//
// RETURNS:
// - seed: Main account private key seed (SUAC...)
// - public: Main account public key (AABC...)
// - signingKey: Signing private key seed (SUAC...)  
// - signingPublic: Signing public key (AABC...)
// - error: nil on success, error on key generation failure
//
// KEY FORMATS:
// All returned keys are in standard NATS string format ready for
// database storage and JWT operations.
//
// SIDE EFFECTS: None (pure key generation)
func (m *Manager) GenerateAccountKeyPair() (seed, public, signingKey, signingPublic string, err error) {
	// Create main account key pair
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		return "", "", "", "", err
	}

	public, err = accountKP.PublicKey()
	if err != nil {
		return "", "", "", "", err
	}

	seedBytes, err := accountKP.Seed()
	if err != nil {
		return "", "", "", "", err
	}
	seed = string(seedBytes)

	// Create signing key pair for the account
	signingKP, err := nkeys.CreateAccount()
	if err != nil {
		return "", "", "", "", err
	}

	signingPublic, err = signingKP.PublicKey()
	if err != nil {
		return "", "", "", "", err
	}

	signingKeyBytes, err := signingKP.Seed()
	if err != nil {
		return "", "", "", "", err
	}
	signingKey = string(signingKeyBytes)

	return seed, public, signingKey, signingPublic, nil
}

// GenerateUserKeyPair generates a key pair for a NATS user.
// Users only need identity keys since their JWTs are signed by
// their account's signing keys.
//
// USER KEY SIMPLICITY:
// Unlike operators and accounts, users don't need signing keys because:
// - Users don't sign JWTs for other entities
// - User JWTs are signed by the account's signing keys
// - Simpler key management for end users
//
// ACCOUNT ASSOCIATION:
// User keys are independent of account assignment. The same user keys
// can be moved between accounts (though this invalidates their JWT
// since each account signs with different keys).
//
// RETURNS:
// - seed: User private key seed (SUUC...)
// - public: User public key (UABC...)  
// - error: nil on success, error on key generation failure
//
// KEY FORMATS:
// Returned keys are in standard NATS string format ready for
// database storage and JWT operations.
//
// SIDE EFFECTS: None (pure key generation)
func (m *Manager) GenerateUserKeyPair() (seed, public string, err error) {
	userKP, err := nkeys.CreateUser()
	if err != nil {
		return "", "", err
	}

	public, err = userKP.PublicKey()
	if err != nil {
		return "", "", err
	}

	seedBytes, err := userKP.Seed()
	if err != nil {
		return "", "", err
	}
	seed = string(seedBytes)

	return seed, public, nil
}

// KeyPairFromSeed creates an nkeys.KeyPair from a seed string.
// This is used for JWT signing operations where we need the cryptographic
// key pair object from stored seed material.
//
// SEED CONVERSION:
// Seeds are stored as strings in the database but the nkeys library
// requires []byte format. This method handles the conversion and
// validation seamlessly.
//
// USAGE PATTERN:
// This method is typically called before JWT signing operations:
//   kp, err := manager.KeyPairFromSeed(account.SigningSeed)
//   jwt, err := userClaims.Encode(kp)
//
// PARAMETERS:
//   - seed: NATS seed string (SUOC..., SUAC..., or SUUC...)
//
// RETURNS:
// - nkeys.KeyPair: Cryptographic key pair ready for signing operations
// - error: nil on success, error if seed invalid or conversion fails
//
// VALIDATION:
// The nkeys library validates seed format and cryptographic integrity
// during conversion, providing error feedback for corrupted seeds.
//
// SIDE EFFECTS: None (pure conversion operation)
func (m *Manager) KeyPairFromSeed(seed string) (nkeys.KeyPair, error) {
	return nkeys.FromSeed([]byte(seed))
}

// GetPrivateKeyFromSeed extracts the private key string from a seed.
// This provides the full private key encoding for database storage.
//
// SEED VS PRIVATE KEY:
// - Seed: Compact representation (SUAC...)
// - Private Key: Full encoding including metadata
// Both contain the same cryptographic material but in different formats.
//
// STORAGE PATTERN:
// The system stores both seeds and private keys for compatibility:
// - Seeds: Used for JWT signing operations  
// - Private Keys: Used for legacy systems or external tools
//
// PARAMETERS:
//   - seed: NATS seed string (SUOC..., SUAC..., or SUUC...)
//
// RETURNS:
// - string: Full private key encoding
// - error: nil on success, error if seed invalid or conversion fails
//
// KEY FORMAT:
// Returns the private key in standard NATS encoding ready for
// database storage or external system integration.
//
// SIDE EFFECTS: None (pure conversion operation)
func (m *Manager) GetPrivateKeyFromSeed(seed string) (string, error) {
	kp, err := nkeys.FromSeed([]byte(seed))
	if err != nil {
		return "", err
	}

	privateKeyBytes, err := kp.PrivateKey()
	if err != nil {
		return "", err
	}

	return string(privateKeyBytes), nil
}
