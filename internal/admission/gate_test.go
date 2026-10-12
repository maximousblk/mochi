package admission

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

const echoPort = 80

func newCA(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func issue(t *testing.T, ca ed25519.PrivateKey, k key.NodePublic, ttl time.Duration) []byte {
	t.Helper()
	c, err := Issue(ca, k, "alice@example.com", time.Now().Add(ttl))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := state.OpenDB(filepath.Join(t.TempDir(), "mochi.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestVerify(t *testing.T) {
	caPub, caPriv := newCA(t)
	_, evil := newCA(t)
	cas := []ed25519.PublicKey{caPub}
	k, other := key.NewNode().Public(), key.NewNode().Public()
	now := time.Now()

	if r, err := Verify(issue(t, caPriv, k, time.Hour), cas, k, now); err != nil || r.ID != "alice@example.com" || !r.CA.Equal(caPub) {
		t.Fatalf("valid cert: %+v err=%v", r, err)
	}
	refuse := func(name string, cert []byte, node key.NodePublic, want string) {
		t.Helper()
		_, err := Verify(cert, cas, node, now)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v; want %q", name, err, want)
		}
	}
	refuse("untrusted CA", issue(t, evil, k, time.Hour), k, "trusted CA")
	refuse("someone else's cert", issue(t, caPriv, other, time.Hour), k, "different node key")
	refuse("expired", issue(t, caPriv, k, -time.Second), k, "expired")

	// Extend the expiry after signing: the signature no longer matches.
	var ct Cert
	dec.Unmarshal(issue(t, caPriv, k, time.Second), &ct)
	var c claims
	dec.Unmarshal(ct.Claims, &c)
	c.Exp += 3600
	ct.Claims, _ = enc.Marshal(c)
	forged, _ := enc.Marshal(ct)
	refuse("forged expiry", forged, k, "trusted CA")
}

func TestIdentity(t *testing.T) {
	a, b := state.NewIdentity(), state.NewIdentity()
	if !state.IdentityFromSeed(a.Seed()).Private().Equal(a.Private()) {
		t.Fatal("identity does not restore from its seed")
	}
	if a.NodeKey(state.RoleServer).Public() == b.NodeKey(state.RoleServer).Public() {
		t.Fatal("two machines share a node key")
	}
	if a.NodeKey(state.RoleServer).Public() == a.NodeKey(state.RoleClient).Public() {
		t.Fatal("one machine serves and dials with the same node key")
	}
	if !state.IdentityFromSeed(a.Seed()).NodeKey(state.RoleClient).Equal(a.NodeKey(state.RoleClient)) {
		t.Fatal("node key is not stable across a restore")
	}
	node := a.NodeKey(state.RoleClient).Public().Raw32()
	if bytes.Contains(a.Public(), node[:]) {
		t.Fatal("a node key leaks the identity")
	}
	// The machine signs approvals as its CA.
	k := key.NewNode().Public()
	cert := issue(t, a.Private(), k, time.Hour)
	if _, err := Verify(cert, []ed25519.PublicKey{a.Public()}, k, time.Now()); err != nil {
		t.Fatalf("self-signed cert refused by its own key: %v", err)
	}
	if _, err := Verify(cert, []ed25519.PublicKey{b.Public()}, k, time.Now()); err == nil {
		t.Fatal("cert from one machine accepted under another's key")
	}
}

type env struct {
	t    *testing.T
	gate *Gate
	addr tailcat.Addr
}

func newEnv(t *testing.T, tweak func(*Gate), trusted ...ed25519.PublicKey) *env {
	t.Helper()
	relay := testharness.Relay(t)
	id := state.NewIdentity()
	g, err := NewGate(openDB(t), id.Private(), trusted)
	if err != nil {
		t.Fatal(err)
	}
	g.ProofTimeout, g.Logf = 3*time.Second, t.Logf
	if tweak != nil {
		tweak(g)
	}
	s := &tailcat.Server{Key: id.NodeKey(state.RoleServer), Region: relay, Logf: testharness.Quiet}
	g.Install(s, func(port uint16) func(net.Conn) {
		if port != echoPort {
			return nil
		}
		return func(c net.Conn) { defer c.Close(); io.Copy(c, c) }
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.Run(runCtx)
	return &env{t: t, gate: g, addr: s.TailcatAddr()}
}

// client is a stock tailcat client; it sends nothing unless asked to Prove.
func (e *env) client(k key.NodePrivate) *tailcat.Client {
	c := &tailcat.Client{Server: e.addr, Key: k, Logf: testharness.Quiet}
	e.t.Cleanup(func() { c.Close() })
	return c
}

func (e *env) row(k key.NodePublic) Client {
	e.gate.mu.Lock()
	defer e.gate.mu.Unlock()
	return e.gate.get(k)
}

// waitState polls until k's row is in state, and returns the row.
func (e *env) waitState(k key.NodePublic, state string, within time.Duration) Client {
	e.t.Helper()
	deadline := time.Now().Add(within)
	for {
		if r := e.row(k); r.State == state {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("row is %q after %v; want %q", e.row(k).State, within, state)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func ctx(d time.Duration) context.Context {
	c, cancel := context.WithTimeout(context.Background(), d)
	_ = cancel // released when the deadline passes
	return c
}

func prove(c *tailcat.Client, cert []byte) error {
	return Prove(ctx(20*time.Second), c, ProofPort, [][]byte{cert})
}

func openEcho(c *tailcat.Client) (net.Conn, error) {
	// 20s: a same-key reconnect to a server that still holds the old
	// tunnel waits out one or more 5s WireGuard handshake retries.
	conn, err := c.DialTCPPort(ctx(20*time.Second), echoPort)
	if err != nil {
		return nil, err
	}
	if err := echo(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func echo(conn net.Conn) error {
	if _, err := conn.Write([]byte("x")); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	_, err := io.ReadFull(conn, make([]byte, 1))
	return err
}

func canUseService(c *tailcat.Client) bool {
	conn, err := openEcho(c)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitClosed reports how long until the server closes conn.
func waitClosed(t *testing.T, conn net.Conn, max time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	conn.SetReadDeadline(start.Add(max))
	io.ReadAll(conn)
	d := time.Since(start)
	if d >= max {
		t.Fatalf("connection still open after %v", max)
	}
	return d
}

func TestAdmission(t *testing.T) {
	caPub, caPriv := newCA(t)
	_, evil := newCA(t)
	e := newEnv(t, nil, caPub)

	t.Run("no_cert_no_service", func(t *testing.T) {
		if canUseService(e.client(key.NewNode())) {
			t.Fatal("unproven key used a service")
		}
	})

	t.Run("prove_then_reconnect_without_proof", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		if err := prove(c, issue(t, caPriv, k.Public(), time.Hour)); err != nil {
			t.Fatal(err)
		}
		if r := e.row(k.Public()); r.State != Approved || r.CA != Fingerprint(caPub) || r.Name != "alice@example.com" {
			t.Fatalf("row = %+v", r)
		}
		if !canUseService(c) {
			t.Fatal("proven key refused")
		}
		c.Close()
		// A fresh tunnel, no certificate: AllowClient and the table only.
		c2 := e.client(k)
		if !canUseService(c2) {
			t.Fatal("proven key refused on reconnect without proof")
		}
		c2.Close() // one live tunnel per key, as a real client keeps
	})

	t.Run("renew_extends_without_dropping", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		if err := prove(c, issue(t, caPriv, k.Public(), 4*time.Second)); err != nil {
			t.Fatal(err)
		}
		conn, err := openEcho(c)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := prove(c, issue(t, caPriv, k.Public(), time.Hour)); err != nil {
			t.Fatalf("renewal: %v", err)
		}
		time.Sleep(6 * time.Second) // past the first certificate's expiry
		if err := echo(conn); err != nil {
			t.Fatalf("connection dropped at the old expiry despite renewal: %v", err)
		}
	})

	t.Run("a_bad_certificate_changes_nothing_for_an_approved_node", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		if err := prove(c, issue(t, caPriv, k.Public(), time.Hour)); err != nil {
			t.Fatal(err)
		}
		before := e.row(k.Public())
		// The reply says where the node stands, not whether the certificate it
		// just sent was any good, and a node that is approved stands fine.
		if err := prove(c, issue(t, evil, k.Public(), time.Hour)); err != nil {
			t.Fatalf("an approved node was turned away over a certificate it did not need: %v", err)
		}
		if after := e.row(k.Public()); after.State != Approved || after.CA != before.CA || after.Name != before.Name || !after.Expires.Equal(before.Expires) {
			t.Fatalf("a certificate from an untrusted key changed the row:\n  before %+v\n  after  %+v", before, after)
		}
		if !canUseService(c) {
			t.Fatal("a bad certificate cost an approved key its access")
		}
	})

	for _, tc := range []struct {
		name, reason string
		cert         func(k key.NodePublic) []byte
	}{
		{"untrusted_ca", "trusted CA", func(k key.NodePublic) []byte { return issue(t, evil, k, time.Hour) }},
		{"someone_elses_cert", "different node key", func(key.NodePublic) []byte { return issue(t, caPriv, key.NewNode().Public(), time.Hour) }},
		{"expired_cert", "expired", func(k key.NodePublic) []byte { return issue(t, caPriv, k, -time.Second) }},
	} {
		t.Run(tc.name+"_is_quarantined_with_the_reason", func(t *testing.T) {
			k := key.NewNode()
			c := e.client(k)
			if err := prove(c, tc.cert(k.Public())); err == nil || canUseService(c) {
				t.Fatal("bad certificate admitted")
			}
			r := e.waitState(k.Public(), Rejected, 10*time.Second)
			if !strings.Contains(r.Reason, tc.reason) {
				t.Fatalf("reason = %q; want it to mention %q", r.Reason, tc.reason)
			}
		})
	}

	t.Run("expiry_closes_and_quarantines", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		// Exp has 1s resolution, so the real lifetime is 3-4s.
		if err := prove(c, issue(t, caPriv, k.Public(), 4*time.Second)); err != nil {
			t.Fatal(err)
		}
		conn, err := openEcho(c)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		t.Logf("poc: open connection closed after %v", waitClosed(t, conn, 10*time.Second).Round(100*time.Millisecond))
		if r := e.row(k.Public()); r.State != Rejected || r.Reason != "certificate expired" {
			t.Fatalf("row = %+v", r)
		}
		c.Close()
		if canUseService(e.client(k)) {
			t.Fatal("expired key used a service")
		}
	})

	t.Run("block_closes_refuses_and_beats_a_valid_cert", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		cert := issue(t, caPriv, k.Public(), time.Hour)
		if err := prove(c, cert); err != nil {
			t.Fatal(err)
		}
		conn, err := openEcho(c)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		e.gate.Block(k.Public())
		t.Logf("poc: open connection closed %v after Block", waitClosed(t, conn, 3*time.Second).Round(time.Millisecond))
		if e.gate.allowClient(k.Public()) {
			t.Fatal("blocked key admitted by AllowClient")
		}
		if err := e.gate.admit(k.Public(), cert); err == nil {
			t.Fatal("blocked key re-approved by its still-valid certificate")
		}
		// An admin's approval lifts the block.
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		if r := e.row(k.Public()); r.State != Approved {
			t.Fatalf("row after Approve = %+v", r)
		}
	})
}

func TestQuarantine(t *testing.T) {
	e := newEnv(t, nil)

	t.Run("plain_client_is_listed_and_the_admin_approves_it", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		// A stock client never sends a certificate.
		if _, err := c.Ping(ctx(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if canUseService(c) {
			t.Fatal("plain client used a service")
		}
		r := e.waitState(k.Public(), Rejected, 10*time.Second)
		if r.Reason != "no certificate" {
			t.Fatalf("reason = %q", r.Reason)
		}
		if got := e.gate.Clients(Rejected); len(got) != 1 || got[0].Key != k.Public() {
			t.Fatalf("UI list = %+v", got)
		}
		c.Close()

		// The admin recognizes the node and approves it: a certificate from
		// the host's own CA, admitted through the same path as any other.
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		r = e.row(k.Public())
		if r.State != Approved || r.CA != e.gate.selfFP || r.Name != "approved on this host" {
			t.Fatalf("row = %+v", r)
		}
		if !canUseService(e.client(k)) {
			t.Fatal("approved plain client refused, and it sends no certificate")
		}
	})

	t.Run("approve_a_key_never_seen", func(t *testing.T) {
		k := key.NewNode()
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		if !canUseService(e.client(k)) {
			t.Fatal("pre-approved key refused")
		}
	})

	t.Run("approve_while_connected_on_probation", func(t *testing.T) {
		k := key.NewNode()
		c := e.client(k)
		if _, err := c.Ping(ctx(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		// The tunnel already exists; only the service guard consults the table.
		if !canUseService(c) {
			t.Fatal("approving a connected node did not take effect")
		}
	})
}

func TestQuarantineIsForgotten(t *testing.T) {
	e := newEnv(t, func(g *Gate) { g.ProofTimeout, g.RejectTTL = 2*time.Second, 3*time.Second })
	k := key.NewNode()
	c := e.client(k)
	if _, err := c.Ping(ctx(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	e.waitState(k.Public(), Rejected, 10*time.Second)
	e.waitState(k.Public(), "", 10*time.Second) // row deleted at its TTL
}

// Trusted keys are fixed at build time. Dropping one from the list and
// restarting rejects the nodes it approved and leaves the rest.
func TestTrustedKeysAreReconciledAtStartup(t *testing.T) {
	ext, extPriv := newCA(t)
	db := openDB(t)
	host := state.NewIdentity()
	g1 := offlineGate(t, db, host, ext)
	byExt, byHost := key.NewNode().Public(), key.NewNode().Public()
	cert, _ := Issue(extPriv, byExt, "alice@example.com", time.Now().Add(time.Hour))
	if err := g1.admit(byExt, cert); err != nil {
		t.Fatal(err)
	}
	if err := g1.Approve(byHost); err != nil {
		t.Fatal(err)
	}

	g2 := offlineGate(t, db, host, ext) // restart, same trusted keys
	g2.MaxProbation = 0
	if !g2.allowClient(byExt) || !g2.allowClient(byHost) {
		t.Fatal("approvals lost across a restart with unchanged trusted keys")
	}

	g3 := offlineGate(t, db, host) // restart without the external key
	g3.MaxProbation = 0
	if g3.allowClient(byExt) {
		t.Fatal("node approved by a no-longer-trusted key still admitted")
	}
	if r := g3.get(byExt); r.State != Rejected || r.Reason != "CA no longer trusted" {
		t.Fatalf("row = %+v", r)
	}
	if !g3.allowClient(byHost) {
		t.Fatal("host-approved node lost access when an external key was dropped")
	}
}

func TestProbationCap(t *testing.T) {
	e := newEnv(t, func(g *Gate) { g.MaxProbation = 1 })
	ping := func(k key.NodePrivate, d time.Duration) bool {
		c := &tailcat.Client{Server: e.addr, Key: k, Logf: testharness.Quiet}
		defer c.Close()
		_, err := c.Ping(ctx(d))
		return err == nil
	}
	// The first contact goes through the relay and sets it up, which can take
	// several seconds, as the other first dials in this package allow.
	if !ping(key.NewNode(), 20*time.Second) {
		t.Fatal("first stranger refused")
	}
	// The one slot is held until the first stranger's proof times out.
	if ping(key.NewNode(), 2*time.Second) {
		t.Fatal("second stranger admitted while the slot was taken")
	}
	// Approved keys never need a slot.
	known := key.NewNode()
	if err := e.gate.Approve(known.Public()); err != nil {
		t.Fatal(err)
	}
	if !ping(known, 5*time.Second) {
		t.Fatal("approved key refused while probation was full")
	}
	time.Sleep(3 * time.Second)
	if !ping(key.NewNode(), 5*time.Second) {
		t.Fatal("stranger refused after the slot freed up")
	}
}

// The remaining tests need no network: they exercise the table.

func offlineGate(t *testing.T, db *sql.DB, self state.Identity, trusted ...ed25519.PublicKey) *Gate {
	t.Helper()
	g, err := NewGate(db, self.Private(), trusted)
	if err != nil {
		t.Fatal(err)
	}
	g.srv = &tailcat.Server{} // never started: DisconnectClient is a no-op
	return g
}

func TestRestart(t *testing.T) {
	db := openDB(t)
	host := state.NewIdentity()
	g1 := offlineGate(t, db, host)
	approved, stranger := key.NewNode().Public(), key.NewNode().Public()
	if err := g1.Approve(approved); err != nil {
		t.Fatal(err)
	}
	if !g1.allowClient(stranger) || g1.strangers() != 1 {
		t.Fatal("stranger did not get a probation slot")
	}

	g2 := offlineGate(t, db, host) // the same host after a restart
	if g2.get(stranger).State != "" {
		t.Fatal("a stale probation row survived the restart")
	}
	g2.MaxProbation = 0 // no slots: only a live approval can pass
	if !g2.allowClient(approved) {
		t.Fatal("approval lost over a restart")
	}

	g3 := offlineGate(t, db, state.NewIdentity()) // a different host reading the same rows
	g3.MaxProbation = 0
	if g3.allowClient(approved) {
		t.Fatal("an approval by another host's CA was honored")
	}
}

func TestRejectedListIsBounded(t *testing.T) {
	g := offlineGate(t, openDB(t), state.NewIdentity())
	g.MaxRejected, g.MaxProbation, g.ProofTimeout = 3, 100, 0
	for range 10 {
		if !g.allowClient(key.NewNode().Public()) {
			t.Fatal("stranger refused")
		}
		g.sweep() // the zero timeout has already passed
	}
	if n := g.count(Rejected); n != 3 {
		t.Fatalf("%d rejected rows; want 3", n)
	}
	if n := g.count(Probation); n != 0 {
		t.Fatalf("%d probation rows left", n)
	}
}
