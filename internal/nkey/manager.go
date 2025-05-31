// Package nkey provides NKey management for NATS JWT authentication
package nkey

import (
	"github.com/nats-io/nkeys"
)

// Manager handles NKey generation and management
// Note: No encryption complexity - keys are stored as plaintext in database
// This follows the simplified architecture approach
type Manager struct{}

// NewManager creates a new NKey manager
func NewManager() *Manager {
	return &Manager{}
}

// GenerateOperatorKeyPair generates a new operator key pair
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

// GenerateAccountKeyPair generates a new account key pair with signing keys
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

// GenerateUserKeyPair generates a new user key pair
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

// KeyPairFromSeed creates a key pair from a seed string
func (m *Manager) KeyPairFromSeed(seed string) (nkeys.KeyPair, error) {
	return nkeys.FromSeed([]byte(seed))
}

// GetPrivateKeyFromSeed extracts the private key from a seed
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
