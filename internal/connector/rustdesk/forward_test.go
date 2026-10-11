package rustdesk

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	"tailscale.com/types/key"
)

// Enabling a host whose direct-IP port is open forwards that port to a local
// address, launches RustDesk on it, and carries traffic both ways until the
// launcher is disabled.
func TestEnableForwardsDirectIPAndLaunchesRustDesk(t *testing.T) {
	relay := testharness.Relay(t)
	argsFile := testharness.FakeRustDesk(t)
	server := testharness.Host(t, relay, map[uint16]func(net.Conn){
		DirectPort: func(c net.Conn) { defer c.Close(); io.Copy(c, c) }, // an echo for the RustDesk port
	})
	host := state.Host{Name: "lab", Address: testharness.Address(server)}
	c := New(nil, admission.Dialing{Key: key.NewNode()})

	if err := c.Enable(host); err != nil {
		t.Fatalf("enable: %v", err)
	}
	local := c.URL(host)
	if !strings.HasPrefix(local, "127.0.0.1:") {
		t.Fatalf("forward address = %q; want a loopback address", local)
	}

	conn, err := net.DialTimeout("tcp", local, 10*time.Second)
	if err != nil {
		t.Fatalf("dial forward: %v", err)
	}
	defer conn.Close()
	conn.Write([]byte("ping"))
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
		t.Fatalf("reply through the forward = %q, %v; want \"ping\"", reply, err)
	}

	// The launched RustDesk was given the same loopback address.
	var launched []string
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(argsFile)
		if err == nil && len(strings.Fields(string(raw))) == 2 {
			launched = strings.Fields(string(raw))
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(launched) != 2 || launched[0] != "--connect" || launched[1] != local {
		t.Fatalf("RustDesk was started with %q; want --connect %s", launched, local)
	}

	if err := c.Disable(host); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if c.URL(host) != "" {
		t.Fatal("forward address still reported after disable")
	}
	if late, err := net.DialTimeout("tcp", local, 2*time.Second); err == nil {
		late.Close()
		t.Fatal("forward still accepts connections after disable")
	}
}

// A host with direct IP off refuses the port: Enable fails with the reason and
// leaves nothing running.
func TestEnableRefusesHostWithDirectIPOff(t *testing.T) {
	relay := testharness.Relay(t)
	testharness.FakeRustDesk(t)
	server := testharness.Host(t, relay, nil) // no RustDesk port served
	host := state.Host{Name: "lab", Address: testharness.Address(server)}
	c := New(nil, admission.Dialing{Key: key.NewNode()})

	err := c.Enable(host)
	if err == nil || !strings.Contains(err.Error(), "does not accept RustDesk direct IP") {
		t.Fatalf("enable error = %v; want the direct-IP refusal", err)
	}
	if c.Enabled(host) || c.URL(host) != "" {
		t.Fatal("a refused host is reported as enabled")
	}
}
