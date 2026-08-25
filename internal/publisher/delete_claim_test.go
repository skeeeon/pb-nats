package publisher

import (
	"testing"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// newOperatorWithSigningKey builds an operator whose identity and signing keys are real,
// distinct nkeys, so a decoded claim's issuer identifies which one signed it.
func newOperatorWithSigningKey(t *testing.T) (*pbtypes.SystemOperatorRecord, string, string) {
	t.Helper()

	identityKP, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatalf("create operator identity: %v", err)
	}
	identityPub, err := identityKP.PublicKey()
	if err != nil {
		t.Fatalf("identity public key: %v", err)
	}
	identitySeed, err := identityKP.Seed()
	if err != nil {
		t.Fatalf("identity seed: %v", err)
	}

	signingKP, err := nkeys.CreatePair(nkeys.PrefixByteOperator)
	if err != nil {
		t.Fatalf("create signing key: %v", err)
	}
	signingPub, err := signingKP.PublicKey()
	if err != nil {
		t.Fatalf("signing public key: %v", err)
	}
	signingSeed, err := signingKP.Seed()
	if err != nil {
		t.Fatalf("signing seed: %v", err)
	}

	pub, priv := pbtypes.NewSigningKeyPair(signingPub, "", string(signingSeed))

	return &pbtypes.SystemOperatorRecord{
		Name:               "test-operator",
		PublicKey:          identityPub,
		Seed:               string(identitySeed),
		SigningKeys:        []pbtypes.SigningKeyPublic{pub},
		SigningKeysPrivate: []pbtypes.SigningKeyPrivate{priv},
	}, identityPub, signingPub
}

// TestBuildAccountDeleteClaim_SignedBySigningKey pins the two properties nats-server
// checks before it will withdraw an account, and the one pb-nats chooses on top.
//
// handleDeleteRequest rejects the claim as "not self signed" unless subject equals
// issuer, and as "not trusted" unless that issuer is in the operator key set. That set is
// the operator subject plus every signing key — except under strict_signing_key_usage,
// which drops the subject. Signing with the identity key therefore works today and stops
// working the moment strict mode is enabled, silently, in the reply envelope. Asserting
// the issuer is the *signing* key is what keeps that from regressing.
func TestBuildAccountDeleteClaim_SignedBySigningKey(t *testing.T) {
	operator, identityPub, signingPub := newOperatorWithSigningKey(t)
	const accountPub = "ATESTACCOUNTPUBLICKEY"

	encoded, err := buildAccountDeleteClaim(operator, accountPub)
	if err != nil {
		t.Fatalf("buildAccountDeleteClaim: %v", err)
	}

	claim, err := jwt.DecodeGeneric(encoded)
	if err != nil {
		t.Fatalf("decode delete claim: %v", err)
	}

	if claim.Issuer != signingPub {
		t.Errorf("issuer = %q, want the operator signing key %q", claim.Issuer, signingPub)
	}
	if claim.Issuer == identityPub {
		t.Error("claim was signed by the operator identity key; strict_signing_key_usage rejects this")
	}
	// "not self signed" — the server compares subject against issuer.
	if claim.Subject != claim.Issuer {
		t.Errorf("subject = %q, issuer = %q; the server requires them to match", claim.Subject, claim.Issuer)
	}

	accounts, ok := claim.Data["accounts"].([]any)
	if !ok {
		t.Fatalf("accounts payload = %#v, want a JSON array", claim.Data["accounts"])
	}
	if len(accounts) != 1 || accounts[0] != accountPub {
		t.Errorf("accounts = %v, want [%s]", accounts, accountPub)
	}
}

// TestBuildAccountDeleteClaim_VerifiesAgainstSigningKey confirms the signature really
// was produced by the signing key's seed, not merely labelled with its public key. A
// claim whose issuer and signature disagree is rejected at decode time by the server,
// which would surface only as a delete that never happens.
func TestBuildAccountDeleteClaim_VerifiesAgainstSigningKey(t *testing.T) {
	operator, _, signingPub := newOperatorWithSigningKey(t)

	encoded, err := buildAccountDeleteClaim(operator, "ATESTACCOUNTPUBLICKEY")
	if err != nil {
		t.Fatalf("buildAccountDeleteClaim: %v", err)
	}

	claim, err := jwt.DecodeGeneric(encoded)
	if err != nil {
		t.Fatalf("decode delete claim: %v", err)
	}
	vr := &jwt.ValidationResults{}
	claim.Validate(vr)
	if !vr.IsEmpty() {
		t.Errorf("claim failed validation: %v", vr.Errors())
	}
	if claim.Issuer != signingPub {
		t.Errorf("issuer = %q, want %q", claim.Issuer, signingPub)
	}
}

// TestBuildAccountDeleteClaim_NoSigningKey guards the sealed-identity case. An operator
// with no signing key must fail loudly here rather than fall back to the identity key —
// the fallback is exactly the behaviour being removed.
func TestBuildAccountDeleteClaim_NoSigningKey(t *testing.T) {
	operator, _, _ := newOperatorWithSigningKey(t)
	operator.SigningKeys = nil
	operator.SigningKeysPrivate = nil

	if _, err := buildAccountDeleteClaim(operator, "ATESTACCOUNTPUBLICKEY"); err == nil {
		t.Fatal("expected an error when the operator has no signing keys")
	}
}
