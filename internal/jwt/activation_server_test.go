package jwt

import (
	"encoding/json"
	"testing"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// These assert nats-server's own decision about a token_req export, because that is
// the thing being relied on: an account that imports a private export it was not
// granted must receive nothing. A claims-only test would pass against an export that
// is still public. Every "cannot" here has a "can" on the same server and export.
func TestActivationTokensAreEnforcedByServer(t *testing.T) {
	nm := nkey.NewManager()
	g := NewGenerator(nm, pbtypes.Options{})

	operatorKP, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatalf("CreateOperator: %v", err)
	}
	operatorPub, _ := operatorKP.PublicKey()
	operatorSeed, _ := operatorKP.Seed()
	operatorJWT, err := jwt.NewOperatorClaims(operatorPub).Encode(operatorKP)
	if err != nil {
		t.Fatalf("encode operator: %v", err)
	}
	operatorClaims, err := jwt.DecodeOperatorClaims(operatorJWT)
	if err != nil {
		t.Fatalf("decode operator: %v", err)
	}

	account := func(name string) *pbtypes.AccountRecord {
		a := newTestAccount(t, nm)
		a.Name = name
		// Zero means zero in a JWT: payload 0 closes the connection on the first
		// message. Limits are not under test.
		a.MaxSubscriptions, a.MaxPayload = -1, -1
		return a
	}
	exporter := account("exporter")
	granted := account("granted")
	ungranted := account("ungranted")
	revoked := account("revoked")

	sign := func(importer *pbtypes.AccountRecord) string {
		token, err := g.GenerateActivationToken(exporter.Seed, importer.PublicKey, "inv.>", "stream")
		if err != nil {
			t.Fatalf("GenerateActivationToken: %v", err)
		}
		return token
	}
	grantedToken := sign(granted)
	revokedToken := sign(revoked)

	export := &pbtypes.AccountExportRecord{
		Name: "inventory", Subject: "inv.>", Type: "stream", TokenReq: true,
		// A cutoff at or after the token's issue time revokes it.
		Revocations: map[string]int64{revoked.PublicKey: time.Now().Add(time.Minute).Unix()},
	}
	importOf := func(token string) []*pbtypes.AccountImportRecord {
		return []*pbtypes.AccountImportRecord{{
			Name: "inventory", Subject: "inv.>", Account: exporter.PublicKey, Type: "stream", Token: token,
		}}
	}

	resolver := &server.MemAccResolver{}
	store := func(a *pbtypes.AccountRecord, exports []*pbtypes.AccountExportRecord, imports []*pbtypes.AccountImportRecord) {
		accountJWT, err := g.GenerateAccountJWT(a, string(operatorSeed), exports, imports)
		if err != nil {
			t.Fatalf("GenerateAccountJWT(%s): %v", a.Name, err)
		}
		if err := resolver.Store(a.PublicKey, accountJWT); err != nil {
			t.Fatalf("store %s: %v", a.Name, err)
		}
	}
	store(exporter, []*pbtypes.AccountExportRecord{export}, nil)
	store(granted, nil, importOf(grantedToken))
	store(ungranted, nil, importOf(""))
	store(revoked, nil, importOf(revokedToken))

	srv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{operatorClaims},
		AccountResolver:  resolver,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats-server not ready")
	}

	all := &pbtypes.RoleRecord{
		Name:                 "all",
		PublishPermissions:   json.RawMessage(`[">"]`),
		SubscribePermissions: json.RawMessage(`[">"]`),
		MaxSubscriptions:     -1, MaxData: -1, MaxPayload: -1,
	}
	connect := func(a *pbtypes.AccountRecord) *nats.Conn {
		seed, public, err := nm.GenerateUserKeyPair()
		if err != nil {
			t.Fatalf("GenerateUserKeyPair: %v", err)
		}
		token, err := g.GenerateUserJWT(&pbtypes.NatsUserRecord{NatsUsername: a.Name, PublicKey: public, Seed: seed}, a, all)
		if err != nil {
			t.Fatalf("GenerateUserJWT(%s): %v", a.Name, err)
		}
		nc, err := nats.Connect(srv.ClientURL(), nats.UserJWTAndSeed(token, seed))
		if err != nil {
			t.Fatalf("connect %s: %v", a.Name, err)
		}
		t.Cleanup(nc.Close)
		return nc
	}

	subscribe := func(a *pbtypes.AccountRecord) *nats.Subscription {
		nc := connect(a)
		sub, err := nc.SubscribeSync("inv.>")
		if err != nil {
			t.Fatalf("subscribe %s: %v", a.Name, err)
		}
		if err := nc.Flush(); err != nil {
			t.Fatalf("flush %s: %v", a.Name, err)
		}
		return sub
	}
	subs := map[string]*nats.Subscription{
		"granted":   subscribe(granted),
		"ungranted": subscribe(ungranted),
		"revoked":   subscribe(revoked),
	}

	pub := connect(exporter)
	if err := pub.Publish("inv.thing.created", []byte("x")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := pub.Flush(); err != nil {
		t.Fatalf("flush publisher: %v", err)
	}

	if _, err := subs["granted"].NextMsg(2 * time.Second); err != nil {
		t.Errorf("the granted importer received nothing (%v): a signed token should admit it", err)
	}
	for _, name := range []string{"ungranted", "revoked"} {
		if msg, err := subs[name].NextMsg(300 * time.Millisecond); err == nil {
			t.Errorf("the %s importer received %q from a token_req export; want nothing", name, msg.Subject)
		}
	}
}
