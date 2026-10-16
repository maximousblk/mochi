package admission

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/maximousblk/mochi/internal/state"
	"github.com/maximousblk/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func echoServe(port uint16) func(net.Conn) {
	if port != echoPort {
		return nil
	}
	return func(c net.Conn) { defer c.Close(); io.Copy(c, c) }
}

// tryService is canUseService with a short dial timeout.
func tryService(c *tailcat.Client, d time.Duration) bool {
	conn, err := c.DialTCPPort(ctx(d), echoPort)
	if err != nil {
		return false
	}
	defer conn.Close()
	return echo(conn) == nil
}

// A server restarted with the same node key, pre-shared key and DERP
// region has the same address, and a node approved before the restart gets
// in afterwards without presenting anything.
func TestServerRestart(t *testing.T) {
	relay := testharness.Relay(t)
	id, psk, db := state.NewIdentity(), tailcat.NewPresharedKey(), openDB(t)
	start := func() (*Gate, *tailcat.Server) {
		g, err := NewGate(db, id.Private(), nil)
		if err != nil {
			t.Fatal(err)
		}
		s := &tailcat.Server{Key: id.NodeKey(state.RoleServer), PresharedKey: psk, Region: relay, Logf: testharness.Quiet}
		g.Install(s, echoServe)
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		return g, s
	}

	g1, s1 := start()
	addr := s1.TailcatAddr()
	k := key.NewNode()
	if err := g1.Approve(k.Public()); err != nil {
		t.Fatal(err)
	}
	c := &tailcat.Client{Server: addr, Key: k, Logf: testharness.Quiet}
	if !canUseService(c) {
		t.Fatal("approved node refused before the restart")
	}
	c.Close()
	s1.Close()

	_, s2 := start()
	defer s2.Close()
	if s2.TailcatAddr() != addr {
		t.Fatal("address changed across a restart with the same key, psk and region")
	}
	c2 := &tailcat.Client{Server: addr, Key: k, Logf: testharness.Quiet}
	defer c2.Close()
	if !canUseService(c2) {
		t.Fatal("approved node refused after the restart")
	}
}

// A machine serves and dials at once: A serves, dials host B, and M dials
// A. Its two tailcat instances need different node keys. On one shared key,
// the relay delivers a packet for the key to only one of its two
// connections, and M's dials to A were lost in 4 of 5 measured runs (see
// PLAN.md). Run with TS_DEBUG_ALWAYS_USE_DERP=1 to take direct paths out of
// the picture.
func TestServeAndDialOnOneMachine(t *testing.T) {
	relay := testharness.Relay(t)
	idA, idB := state.NewIdentity(), state.NewIdentity()
	mk := func(id state.Identity) *Gate {
		g, err := NewGate(openDB(t), id.Private(), nil)
		if err != nil {
			t.Fatal(err)
		}
		s := &tailcat.Server{Key: id.NodeKey(state.RoleServer), Region: relay, Logf: testharness.Quiet}
		g.Install(s, echoServe)
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		g.srv = s
		return g
	}
	gA, gB := mk(idA), mk(idB)
	m := key.NewNode()
	if err := gA.Approve(m.Public()); err != nil {
		t.Fatal(err)
	}
	if err := gB.Approve(idA.NodeKey(state.RoleClient).Public()); err != nil {
		t.Fatal(err)
	}
	aToB := &tailcat.Client{Server: gB.srv.TailcatAddr(), Key: idA.NodeKey(state.RoleClient), Logf: testharness.Quiet}
	mToA := &tailcat.Client{Server: gA.srv.TailcatAddr(), Key: m, Logf: testharness.Quiet}
	t.Cleanup(func() { aToB.Close(); mToA.Close() })

	const n = 5
	var okAB, okMA int
	for range n {
		var wg sync.WaitGroup
		var mu sync.Mutex
		wg.Add(2)
		go func() {
			defer wg.Done()
			if tryService(aToB, 6*time.Second) {
				mu.Lock()
				okAB++
				mu.Unlock()
			}
		}()
		go func() {
			defer wg.Done()
			if tryService(mToA, 6*time.Second) {
				mu.Lock()
				okMA++
				mu.Unlock()
			}
		}()
		wg.Wait()
		time.Sleep(500 * time.Millisecond)
	}
	if okAB != n || okMA != n {
		t.Fatalf("a machine serving and dialing lost traffic: A->B %d/%d, M->A %d/%d", okAB, n, okMA, n)
	}
}
