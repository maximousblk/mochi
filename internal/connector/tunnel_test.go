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
