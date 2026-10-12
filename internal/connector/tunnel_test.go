package connector

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

const echoPort uint16 = 7

// openTestTunnel tunnels to echoPort on a host that echoes, from a new client.
func openTestTunnel(t *testing.T) *Tunnel {
	t.Helper()
	relay := testharness.Relay(t)
	host := testharness.Host(t, relay, map[uint16]func(net.Conn){
		echoPort: func(c net.Conn) { defer c.Close(); io.Copy(c, c) },
	})
	client := admission.Dialing{Key: key.NewNode()}.Dialer(tailcat.Addr(testharness.Address(host)))
	tun, err := OpenTunnel(client, echoPort)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Disconnect() })
	return tun
}

func dialTunnel(t *testing.T, tun *Tunnel) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tun.Port()), 10*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	return conn
}

// waitIdle waits until no carried connection is open.
func waitIdle(t *testing.T, tun *Tunnel) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		tun.mu.Lock()
		open := tun.open
		tun.mu.Unlock()
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the tunnel still has an open connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Bytes go to the host and come back through the tunnel, and LastUsed follows
// the data: it is now while a connection is open, and stays put once it is idle.
func TestTunnelCarriesBytesAndTracksLastUsed(t *testing.T) {
	tun := openTestTunnel(t)
	created := tun.LastUsed()
	time.Sleep(20 * time.Millisecond)

	conn := dialTunnel(t, tun)
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
		t.Fatalf("reply through the tunnel = %q, %v; want \"ping\"", reply, err)
	}
	if idle := time.Since(tun.LastUsed()); idle > time.Second {
		t.Fatalf("LastUsed is %v old while a connection is open; want now", idle)
	}

	conn.Close()
	waitIdle(t, tun)
	last := tun.LastUsed()
	if !last.After(created) {
		t.Fatalf("LastUsed = %v did not advance past the time the tunnel opened, %v", last, created)
	}
	time.Sleep(50 * time.Millisecond)
	if got := tun.LastUsed(); !got.Equal(last) {
		t.Fatalf("LastUsed moved from %v to %v while the tunnel was idle", last, got)
	}
}

// One machine's launchers can all be connected to a host at once, as SSH, WebDAV
// and RustDesk are. Their tunnels dial with the same node key, and each keeps
// carrying data while the others do. Closing one leaves the rest working.
func TestTunnelsToOneHostWorkTogether(t *testing.T) {
	relay := testharness.Relay(t)
	host := testharness.Host(t, relay, map[uint16]func(net.Conn){
		echoPort: func(c net.Conn) { defer c.Close(); io.Copy(c, c) },
	})
	dialing := admission.Dialing{Key: key.NewNode()}
	addr := tailcat.Addr(testharness.Address(host))
	var tuns []*Tunnel
	for range 3 {
		tun, err := OpenTunnel(dialing.Dialer(addr), echoPort)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tun.Disconnect() })
		tuns = append(tuns, tun)
	}
	var conns []net.Conn
	for _, tun := range tuns {
		conn := dialTunnel(t, tun)
		defer conn.Close()
		conns = append(conns, conn)
	}
	echo := func(i int, round int) {
		t.Helper()
		msg := fmt.Sprintf("%d:%02d", i, round)
		if _, err := conns[i].Write([]byte(msg)); err != nil {
			t.Fatalf("tunnel %d round %d: write: %v", i, round, err)
		}
		reply := make([]byte, len(msg))
		if _, err := io.ReadFull(conns[i], reply); err != nil || string(reply) != msg {
			t.Fatalf("tunnel %d round %d: reply %q, %v; want %q", i, round, reply, err, msg)
		}
	}
	// Past one WireGuard handshake retry, so a client whose session another
	// took over would have stalled by the end.
	deadline := time.Now().Add(7 * time.Second)
	for round := 0; time.Now().Before(deadline); round++ {
		for i := range conns {
			echo(i, round%100)
		}
		time.Sleep(250 * time.Millisecond)
	}

	if err := tuns[0].Disconnect(); err != nil {
		t.Fatal(err)
	}
	conns = conns[1:]
	for i := range conns {
		echo(i, 0)
	}
	late := dialTunnel(t, tuns[1])
	defer late.Close()
	conns = append(conns, late)
	echo(len(conns)-1, 1)
}

// Disconnect closes open connections and the listener, and may be repeated.
func TestTunnelDisconnectClosesConnections(t *testing.T) {
	tun := openTestTunnel(t)
	conn := dialTunnel(t, tun)
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reply before Disconnect: %v", err)
	}

	if err := tun.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if err := tun.Disconnect(); err != nil {
		t.Fatalf("second Disconnect: %v", err)
	}
	if _, err := conn.Read(reply); err == nil {
		t.Fatal("an open connection survived Disconnect")
	}
	if late, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tun.Port()), 2*time.Second); err == nil {
		late.Close()
		t.Fatal("the tunnel still accepts connections after Disconnect")
	}
}
