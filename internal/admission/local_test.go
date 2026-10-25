package admission

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/maximousblk/mochi/internal/state"
	"github.com/maximousblk/mochi/internal/testharness"
	"tailscale.com/types/key"
)

// A node this host approved itself keeps its approval: the host signs a new
// short-lived certificate for it before anyone sees the old one expire, and
// an open connection is never cut. Block is the only way to take it back,
// and a blocked node is not renewed.
func TestLocalApprovalRenews(t *testing.T) {
	e := newEnv(t, func(g *Gate) { g.LocalTTL = 4 * time.Second }) // Exp has 1s resolution: 3-4s
	k := key.NewNode()
	if err := e.gate.Approve(k.Public()); err != nil {
		t.Fatal(err)
	}
	first := e.row(k.Public()).Expires
	c := e.client(k)
	conn, err := openEcho(c)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(9 * time.Second) // more than two certificate lifetimes
	if err := echo(conn); err != nil {
		t.Fatalf("an open connection did not survive the renewal: %v", err)
	}
	r := e.row(k.Public())
	if r.State != Approved || r.CA != e.gate.selfFP || !r.Expires.After(first) || !r.Expires.After(time.Now()) {
		t.Fatalf("row = %+v; want approved by this host with a later expiry than %v", r, first)
	}
	conn.Close()
	c.Close()
	if !canUseService(e.client(k)) { // a new connection, no certificate
		t.Fatal("renewed node refused")
	}

	e.gate.Block(k.Public())
	time.Sleep(6 * time.Second)
	if r := e.row(k.Public()); r.State != Blocked {
		t.Fatalf("a blocked node was renewed: %+v", r)
	}
}

// A node this host approved is renewed the moment anyone asks, even if the
// sweep has not run, as after a restart that came long after the expiry.
func TestLocalApprovalRenewsOnDemand(t *testing.T) {
	g := offlineGate(t, openDB(t), state.NewIdentity())
	k := key.NewNode().Public()
	if err := g.Approve(k); err != nil {
		t.Fatal(err)
	}
	record, err := g.app.FindFirstRecordByData("clients", "node_key", k.String())
	if err != nil {
		t.Fatal(err)
	}
	record.Set("expires", time.Now().Add(-48*time.Hour))
	if err := g.app.Save(record); err != nil {
		t.Fatal(err)
	}
	g.MaxProbation = 0 // no slots: only an approval can pass
	if !g.allowClient(k) {
		t.Fatal("a host-approved node was refused after its certificate ran out")
	}
	if r := g.get(k); r.State != Approved || !r.Expires.After(time.Now()) || r.CA != g.selfFP {
		t.Fatalf("row = %+v", r)
	}
}

// A node that was approved before is known. Strangers can hold every
// probation slot and the whole quarantine list without keeping it from
// getting a tunnel to renew through, or evicting its row.
func TestKnownNodesAreNotStarvedByStrangers(t *testing.T) {
	ext, extPriv := newCA(t)
	g := offlineGate(t, openDB(t), state.NewIdentity(), ext)
	g.MaxRejected, g.MaxProbation, g.ProofTimeout = 2, 3, 0

	known := key.NewNode().Public()
	cert, _ := Issue(extPriv, known, "alice@example.com", time.Now().Add(time.Second))
	if err := g.admit(known, cert); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	g.sweep() // the certificate has expired
	r := g.get(known)
	if r.State != Rejected || r.Reason != "certificate expired" || r.CA != Fingerprint(ext) || r.Name != "alice@example.com" {
		t.Fatalf("expired row = %+v; want rejected, still attributed to its CA and name", r)
	}

	// Strangers fill every slot...
	for range g.MaxProbation {
		if !g.allowClient(key.NewNode().Public()) {
			t.Fatal("stranger refused while slots were free")
		}
	}
	if g.allowClient(key.NewNode().Public()) {
		t.Fatal("a stranger got a slot beyond the cap")
	}
	// ...and the known node still gets in.
	if !g.allowClient(known) {
		t.Fatal("a known node was refused because strangers hold the slots")
	}
	if g.strangers() != g.MaxProbation {
		t.Fatalf("a known node took a stranger's slot: %d of %d", g.strangers(), g.MaxProbation)
	}

	// Then the strangers time out, and many more come and go.
	for range 10 {
		g.sweep()
		g.allowClient(key.NewNode().Public())
	}
	g.sweep()
	if g.count(Rejected) > g.MaxRejected+1 {
		t.Fatalf("%d rejected rows: the stranger list is not bounded", g.count(Rejected))
	}
	g.setState(known, Rejected, "certificate expired", time.Now().Add(time.Hour)) // as its probation lapsing would
	g.reject(key.NewNode().Public(), "no certificate")
	if r := g.get(known); r.State == "" || r.CA != Fingerprint(ext) {
		t.Fatalf("a flood of strangers evicted a known node: %+v", r)
	}
}

// Presenting a new certificate from the same CA works from the tunnel a known
// node got that way, and the approval it earns is attributed to that CA.
func TestKnownNodeRenewsThroughAFullProbation(t *testing.T) {
	ext, extPriv := newCA(t)
	e := newEnv(t, func(g *Gate) { g.MaxProbation = 1 }, ext)
	k := key.NewNode()

	// The certificate is made before the tunnel exists, and the first dial can
	// take several seconds, so it must outlast that.
	d := NewDialer(e.addr, k, func() [][]byte { return [][]byte{issue(t, extPriv, k.Public(), 9*time.Second)} }, testharness.Quiet)
	t.Cleanup(func() { d.Close() })
	conn, err := d.DialTCPPort(ctx(20*time.Second), echoPort)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	e.waitState(k.Public(), Rejected, 20*time.Second) // the certificate runs out
	d.Close()

	// A stranger now holds the only slot.
	stranger := key.NewNode()
	sc := e.client(stranger)
	if _, err := sc.Ping(ctx(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if e.gate.strangers() != 1 {
		t.Fatalf("strangers = %d; want the one slot taken", e.gate.strangers())
	}

	// A second stranger is refused; the known node is not.
	other := &Dialer{Client: e.client(key.NewNode())}
	if _, err := other.Client.Ping(ctx(3 * time.Second)); err == nil {
		t.Fatal("a second stranger got a slot")
	}
	d2 := NewDialer(e.addr, k, func() [][]byte { return [][]byte{issue(t, extPriv, k.Public(), time.Hour)} }, testharness.Quiet)
	t.Cleanup(func() { d2.Close() })
	conn, err = d2.DialTCPPort(ctx(20*time.Second), echoPort)
	if err != nil {
		t.Fatalf("a known node could not renew while strangers held every slot: %v", err)
	}
	conn.Close()
	if r := e.row(k.Public()); r.State != Approved || r.CA != Fingerprint(ext) {
		t.Fatalf("row = %+v", r)
	}
	_ = ed25519.PublicKey(nil)
}
