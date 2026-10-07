package jwt

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// These tests assert the server's trust decision, not the claims. The claims are one
// layer below the gap: an empty allow list in a user JWT reads as "nothing granted" but
// nats-server treats it as "no restriction", so a claims-only test of fail-closed
// permissions passes against a JWT that can publish anywhere in its account.

// trustedServer is an operator-mode nats-server with one account preloaded into a
// memory resolver, and a generator able to mint users for that account.
type trustedServer struct {
	srv     *server.Server
	gen     *Generator
	account *pbtypes.AccountRecord
}

func startTrustedServer(t *testing.T) *trustedServer {
	t.Helper()
	nm := nkey.NewManager()
	account := newTestAccount(t, nm)

	operatorKP, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatalf("CreateOperator: %v", err)
	}
	operatorPub, _ := operatorKP.PublicKey()
	operatorJWT, err := jwt.NewOperatorClaims(operatorPub).Encode(operatorKP)
	if err != nil {
		t.Fatalf("encode operator: %v", err)
	}
	operatorClaims, err := jwt.DecodeOperatorClaims(operatorJWT)
	if err != nil {
		t.Fatalf("decode operator: %v", err)
	}

	accountClaims := jwt.NewAccountClaims(account.PublicKey)
	accountClaims.SigningKeys.Add(account.LatestSigningKey().PublicKey)
	accountJWT, err := accountClaims.Encode(operatorKP)
	if err != nil {
		t.Fatalf("encode account: %v", err)
	}

	resolver := &server.MemAccResolver{}
	if err := resolver.Store(account.PublicKey, accountJWT); err != nil {
		t.Fatalf("store account: %v", err)
	}

	srv, err := server.NewServer(&server.Options{
		Host:             "127.0.0.1",
		Port:             -1,
		NoLog:            true,
		NoSigs:           true,
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

	return &trustedServer{srv: srv, gen: NewGenerator(nm, pbtypes.Options{}), account: account}
}

// client is a connection whose asynchronous permission violations are collected,
// since the server reports them as -ERR after the publish or subscribe has "succeeded".
type client struct {
	nc   *nats.Conn
	errs chan error
}

func (ts *trustedServer) connect(t *testing.T, name string, role *pbtypes.RoleRecord) *client {
	t.Helper()
	nm := nkey.NewManager()
	seed, public, err := nm.GenerateUserKeyPair()
	if err != nil {
		t.Fatalf("GenerateUserKeyPair: %v", err)
	}
	user := &pbtypes.NatsUserRecord{NatsUsername: name, PublicKey: public, Seed: seed}
	// Zero-valued limits mean zero, not unlimited; limits are not under test here.
	role.MaxSubscriptions, role.MaxData, role.MaxPayload = -1, -1, -1
	token, err := ts.gen.GenerateUserJWT(user, ts.account, role)
	if err != nil {
		t.Fatalf("GenerateUserJWT(%s): %v", name, err)
	}

	c := &client{errs: make(chan error, 16)}
	c.nc, err = nats.Connect(ts.srv.ClientURL(),
		nats.UserJWTAndSeed(token, seed),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			c.errs <- err
		}),
	)
	if err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}
	t.Cleanup(c.nc.Close)
	return c
}

// violation flushes the connection and reports whether the server rejected anything
// sent since the last call.
func (c *client) violation(t *testing.T) error {
	t.Helper()
	if err := c.nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case err := <-c.errs:
		return err
	case <-time.After(250 * time.Millisecond):
		return nil
	}
}

func (c *client) canPublish(t *testing.T, subject string) bool {
	t.Helper()
	if err := c.nc.Publish(subject, []byte("x")); err != nil {
		t.Fatalf("publish %s: %v", subject, err)
	}
	return !isPermissionViolation(c.violation(t))
}

func (c *client) canSubscribe(t *testing.T, subject string) bool {
	t.Helper()
	sub, err := c.nc.SubscribeSync(subject)
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	defer sub.Unsubscribe()
	return !isPermissionViolation(c.violation(t))
}

func isPermissionViolation(err error) bool {
	return err != nil && (errors.Is(err, nats.ErrPermissionViolation) ||
		strings.Contains(strings.ToLower(err.Error()), "permissions violation"))
}

func TestEmptyRoleIsDeniedByServer(t *testing.T) {
	ts := startTrustedServer(t)
	empty := ts.connect(t, "empty", &pbtypes.RoleRecord{Name: "empty"})
	scoped := ts.connect(t, "scoped", &pbtypes.RoleRecord{
		Name:                 "scoped",
		PublishPermissions:   json.RawMessage(`["events.>"]`),
		SubscribePermissions: json.RawMessage(`["events.>"]`),
	})

	// Each "cannot" is paired with a "can": a denial that also holds for a role
	// that should succeed is a broken harness, not a working permission.
	for _, subject := range []string{"acc.t1.door.d1.cmd.grant", "$KV.dashboards.x", "events.a"} {
		if empty.canPublish(t, subject) {
			t.Errorf("empty role published to %q; want permissions violation", subject)
		}
	}
	if !scoped.canPublish(t, "events.a") {
		t.Error("scoped role could not publish to events.a")
	}
	if scoped.canPublish(t, "acc.t1.door.d1.cmd.grant") {
		t.Error("scoped role published outside events.>")
	}

	for _, subject := range []string{">", "_INBOX.>", "events.a"} {
		if empty.canSubscribe(t, subject) {
			t.Errorf("empty role subscribed to %q; want permissions violation", subject)
		}
	}
	if !scoped.canSubscribe(t, "events.a") {
		t.Error("scoped role could not subscribe to events.a")
	}
}

// A role with subscribe permissions, no publish permissions and allow_response is a
// reply-only service. Denying all publishing must not take its replies with it.
func TestEmptyPublishStillAllowsResponses(t *testing.T) {
	ts := startTrustedServer(t)
	responder := ts.connect(t, "responder", &pbtypes.RoleRecord{
		Name:                 "responder",
		SubscribePermissions: json.RawMessage(`["svc.>"]`),
		AllowResponse:        true,
	})
	requester := ts.connect(t, "requester", &pbtypes.RoleRecord{
		Name:                 "requester",
		PublishPermissions:   json.RawMessage(`["svc.>"]`),
		SubscribePermissions: json.RawMessage(`["_INBOX.>"]`),
	})

	if _, err := responder.nc.Subscribe("svc.echo", func(m *nats.Msg) { _ = m.Respond(m.Data) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := responder.violation(t); err != nil {
		t.Fatalf("responder subscribe rejected: %v", err)
	}

	reply, err := requester.nc.Request("svc.echo", []byte("ping"), 2*time.Second)
	if err != nil {
		t.Fatalf("request: %v (responder violation: %v)", err, responder.violation(t))
	}
	if string(reply.Data) != "ping" {
		t.Errorf("reply = %q, want ping", reply.Data)
	}

	if responder.canPublish(t, "svc.other") {
		t.Error("reply-only role published to svc.other")
	}
}
