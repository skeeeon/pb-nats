package jwt

import (
	"encoding/json"
	"testing"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

func TestLimitValue(t *testing.T) {
	tests := []struct {
		in   int64
		want int64
	}{
		{-1, jwt.NoLimit},
		{-5, jwt.NoLimit},
		{0, 0},
		{42, 42},
	}
	for _, tt := range tests {
		if got := limitValue(tt.in); got != tt.want {
			t.Errorf("limitValue(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestIsSystemUser(t *testing.T) {
	g := NewGenerator(nkey.NewManager(), pbtypes.Options{})

	user := &pbtypes.NatsUserRecord{NatsUsername: "sys"}
	account := &pbtypes.AccountRecord{ID: "sysacct123"}

	// Before the system account ID is known, nothing is a system user
	if g.isSystemUser(user, account) {
		t.Error("isSystemUser true before SetSystemAccountID")
	}

	g.SetSystemAccountID("sysacct123")
	if !g.isSystemUser(user, account) {
		t.Error("isSystemUser false for sys user in system account")
	}

	if g.isSystemUser(&pbtypes.NatsUserRecord{NatsUsername: "alice"}, account) {
		t.Error("isSystemUser true for non-sys username")
	}
	if g.isSystemUser(user, &pbtypes.AccountRecord{ID: "otheracct"}) {
		t.Error("isSystemUser true for non-system account")
	}
}

// newTestAccount generates a real account with one signing key.
func newTestAccount(t *testing.T, nm *nkey.Manager) *pbtypes.AccountRecord {
	t.Helper()
	seed, public, signingSeed, signingPublic, err := nm.GenerateAccountKeyPair()
	if err != nil {
		t.Fatalf("GenerateAccountKeyPair: %v", err)
	}
	pub, priv := pbtypes.NewSigningKeyPair(signingPublic, "", signingSeed)
	return &pbtypes.AccountRecord{
		ID:                 "acct1",
		Name:               "Test Account",
		PublicKey:          public,
		Seed:               seed,
		SigningKeys:        []pbtypes.SigningKeyPublic{pub},
		SigningKeysPrivate: []pbtypes.SigningKeyPrivate{priv},
		Active:             true,
		MaxConnections:     -1,
		MaxSubscriptions:   100,
		MaxData:            -1,
		MaxPayload:         0,
	}
}

func TestGenerateUserJWT(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{
		DefaultPublishPermissions:   []string{">"},
		DefaultSubscribePermissions: []string{">", "_INBOX.>"},
	})

	account := newTestAccount(t, nm)

	userSeed, userPublic, err := nm.GenerateUserKeyPair()
	if err != nil {
		t.Fatalf("GenerateUserKeyPair: %v", err)
	}
	user := &pbtypes.NatsUserRecord{
		NatsUsername:       "alice",
		PublicKey:          userPublic,
		Seed:               userSeed,
		BearerToken:        true,
		PublishPermissions: json.RawMessage(`["user.extra.>"]`),
	}

	role := &pbtypes.RoleRecord{
		Name:                   "test_role",
		PublishPermissions:     json.RawMessage(`["events.>"]`),
		SubscribePermissions:   json.RawMessage(`["events.>", "_INBOX.>"]`),
		PublishDenyPermissions: json.RawMessage(`["events.secret.>"]`),
		AllowResponse:          true,
		AllowResponseMax:       -1,
		MaxSubscriptions:       50,
		MaxData:                -1,
		MaxPayload:             1024,
	}

	token, err := g.GenerateUserJWT(user, account, role)
	if err != nil {
		t.Fatalf("GenerateUserJWT: %v", err)
	}

	claims, err := jwt.DecodeUserClaims(token)
	if err != nil {
		t.Fatalf("DecodeUserClaims: %v", err)
	}

	if claims.Name != "alice" {
		t.Errorf("Name = %q, want alice", claims.Name)
	}
	if claims.IssuerAccount != account.PublicKey {
		t.Errorf("IssuerAccount = %q, want %q", claims.IssuerAccount, account.PublicKey)
	}
	if !claims.BearerToken {
		t.Error("BearerToken not set")
	}

	// Role + user publish permissions are unioned
	if !claims.Permissions.Pub.Allow.Contains("events.>") || !claims.Permissions.Pub.Allow.Contains("user.extra.>") {
		t.Errorf("Pub.Allow = %v, want union of role and user permissions", claims.Permissions.Pub.Allow)
	}
	if !claims.Permissions.Pub.Deny.Contains("events.secret.>") {
		t.Errorf("Pub.Deny = %v, missing role deny", claims.Permissions.Pub.Deny)
	}
	if claims.Permissions.Resp == nil || claims.Permissions.Resp.MaxMsgs != jwt.NoLimit {
		t.Errorf("Resp = %+v, want unlimited response permission", claims.Permissions.Resp)
	}

	// Role limits
	if claims.Limits.Subs != 50 {
		t.Errorf("Limits.Subs = %d, want 50", claims.Limits.Subs)
	}
	if claims.Limits.Data != jwt.NoLimit {
		t.Errorf("Limits.Data = %d, want NoLimit", claims.Limits.Data)
	}
	if claims.Limits.Payload != 1024 {
		t.Errorf("Limits.Payload = %d, want 1024", claims.Limits.Payload)
	}

	// JWT must be signed by the account's signing key
	signingKey := account.LatestSigningKey()
	if claims.Issuer != signingKey.PublicKey {
		t.Errorf("Issuer = %q, want signing key %q", claims.Issuer, signingKey.PublicKey)
	}
}

func TestGenerateUserJWTDefaultPermissions(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{
		DefaultPublishPermissions:   []string{"default.pub.>"},
		DefaultSubscribePermissions: []string{"default.sub.>"},
	})

	account := newTestAccount(t, nm)
	_, userPublic, err := nm.GenerateUserKeyPair()
	if err != nil {
		t.Fatalf("GenerateUserKeyPair: %v", err)
	}

	user := &pbtypes.NatsUserRecord{NatsUsername: "bob", PublicKey: userPublic}
	role := &pbtypes.RoleRecord{Name: "empty_role"}

	token, err := g.GenerateUserJWT(user, account, role)
	if err != nil {
		t.Fatalf("GenerateUserJWT: %v", err)
	}
	claims, err := jwt.DecodeUserClaims(token)
	if err != nil {
		t.Fatalf("DecodeUserClaims: %v", err)
	}

	if !claims.Permissions.Pub.Allow.Contains("default.pub.>") {
		t.Errorf("Pub.Allow = %v, want defaults applied", claims.Permissions.Pub.Allow)
	}
	if !claims.Permissions.Sub.Allow.Contains("default.sub.>") {
		t.Errorf("Sub.Allow = %v, want defaults applied", claims.Permissions.Sub.Allow)
	}
}

func TestGenerateAccountJWT(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{})

	// Operator signing key signs account JWTs
	_, _, operatorSigningSeed, operatorSigningPublic, err := nm.GenerateOperatorKeyPair()
	if err != nil {
		t.Fatalf("GenerateOperatorKeyPair: %v", err)
	}

	account := newTestAccount(t, nm)
	exports := []*pbtypes.AccountExportRecord{
		{Name: "events", Subject: "events.>", Type: "stream", Advertise: true},
		{Name: "api", Subject: "api.requests", Type: "service", ResponseType: "Stream", ResponseThreshold: 500},
	}
	imports := []*pbtypes.AccountImportRecord{
		{Name: "weather", Subject: "weather.>", Account: "AOTHERACCOUNT", Type: "stream", LocalSubject: "ext.weather.>"},
	}

	token, err := g.GenerateAccountJWT(account, operatorSigningSeed, exports, imports)
	if err != nil {
		t.Fatalf("GenerateAccountJWT: %v", err)
	}

	claims, err := jwt.DecodeAccountClaims(token)
	if err != nil {
		t.Fatalf("DecodeAccountClaims: %v", err)
	}

	if claims.Name != "test_account" {
		t.Errorf("Name = %q, want test_account", claims.Name)
	}
	if claims.Issuer != operatorSigningPublic {
		t.Errorf("Issuer = %q, want operator signing key %q", claims.Issuer, operatorSigningPublic)
	}
	if len(claims.Exports) != 2 {
		t.Errorf("Exports = %d, want 2", len(claims.Exports))
	}
	if len(claims.Imports) != 1 {
		t.Errorf("Imports = %d, want 1", len(claims.Imports))
	}

	// Limits: -1 → NoLimit, positive preserved, 0 → disabled
	if claims.Limits.AccountLimits.Conn != jwt.NoLimit {
		t.Errorf("Conn = %d, want NoLimit", claims.Limits.AccountLimits.Conn)
	}
	if claims.Limits.NatsLimits.Subs != 100 {
		t.Errorf("Subs = %d, want 100", claims.Limits.NatsLimits.Subs)
	}
	if claims.Limits.NatsLimits.Payload != 0 {
		t.Errorf("Payload = %d, want 0 (disabled)", claims.Limits.NatsLimits.Payload)
	}

	// Account signing key embedded
	if !claims.SigningKeys.Contains(account.SigningKeys[0].PublicKey) {
		t.Error("account signing key not embedded in JWT")
	}
}

func TestGenerateUserJWTNoSigningKeys(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{})

	account := &pbtypes.AccountRecord{ID: "acct", Name: "No Keys"}
	user := &pbtypes.NatsUserRecord{NatsUsername: "x", PublicKey: "UABC"}
	role := &pbtypes.RoleRecord{Name: "r"}

	if _, err := g.GenerateUserJWT(user, account, role); err == nil {
		t.Error("expected error for account without signing keys, got nil")
	}
}

func TestGenerateCredsFile(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{})

	account := newTestAccount(t, nm)
	userSeed, userPublic, err := nm.GenerateUserKeyPair()
	if err != nil {
		t.Fatalf("GenerateUserKeyPair: %v", err)
	}
	user := &pbtypes.NatsUserRecord{NatsUsername: "carol", PublicKey: userPublic, Seed: userSeed}
	role := &pbtypes.RoleRecord{Name: "r"}

	token, err := g.GenerateUserJWT(user, account, role)
	if err != nil {
		t.Fatalf("GenerateUserJWT: %v", err)
	}
	user.JWT = token

	creds, err := g.GenerateCredsFile(user)
	if err != nil {
		t.Fatalf("GenerateCredsFile: %v", err)
	}
	if creds == "" {
		t.Fatal("empty creds file")
	}

	// Missing JWT or seed must fail
	if _, err := g.GenerateCredsFile(&pbtypes.NatsUserRecord{Seed: userSeed}); err == nil {
		t.Error("expected error for missing JWT")
	}
	if _, err := g.GenerateCredsFile(&pbtypes.NatsUserRecord{JWT: token}); err == nil {
		t.Error("expected error for missing seed")
	}
}
