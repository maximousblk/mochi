package rustdesk

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	"tailscale.com/types/key"
)

// Connecting to a host whose direct-IP port is open tunnels to that port and
// launches RustDesk on the tunnel. The tunnel carries traffic both ways until it
// is disconnected.
func TestConnectTunnelsDirectIPAndLaunchesRustDesk(t *testing.T) {
	relay := testharness.Relay(t)
	argsFile := testharness.FakeRustDesk(t)
	server := testharness.Host(t, relay, map[uint16]func(net.Conn){
		DirectPort: func(c net.Conn) { defer c.Close(); io.Copy(c, c) }, // an echo for the RustDesk port
	})
	host := state.Host{Name: "lab", Address: testharness.Address(server)}
	c := New(connector.NewPorts(), nil, admission.Dialing{Key: key.NewNode()})

	client, err := c.Connect(host, connector.Config{"port": fmt.Sprint(DirectPort)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	local := fmt.Sprintf("127.0.0.1:%d", client.(*connector.Tunnel).Port())

	conn, err := net.DialTimeout("tcp", local, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()
	conn.Write([]byte("ping"))
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
		t.Fatalf("reply through the tunnel = %q, %v; want \"ping\"", reply, err)
	}

	// The launched RustDesk was given the tunnel's loopback address.
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

	if err := client.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if late, err := net.DialTimeout("tcp", local, 2*time.Second); err == nil {
		late.Close()
		t.Fatal("the tunnel still accepts connections after disconnect")
	}
}

// A host with direct IP off refuses the port: Connect fails with the reason and
// leaves nothing running.
func TestConnectRefusesHostWithDirectIPOff(t *testing.T) {
	relay := testharness.Relay(t)
	testharness.FakeRustDesk(t)
	server := testharness.Host(t, relay, nil) // no RustDesk port served
	host := state.Host{Name: "lab", Address: testharness.Address(server)}
	c := New(connector.NewPorts(), nil, admission.Dialing{Key: key.NewNode()})

	client, err := c.Connect(host, connector.Config{"port": fmt.Sprint(DirectPort)})
	if err == nil || !strings.Contains(err.Error(), "does not accept RustDesk direct IP") {
		t.Fatalf("connect error = %v; want the direct-IP refusal", err)
	}
	if client != nil {
		t.Fatal("a refused host returned a client")
	}
}

// When RustDesk cannot be started, Connect disconnects the tunnel it opened and
// returns the error.
func TestConnectDisconnectsWhenRustDeskCannotStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test installs a file without the execute bit, which Windows does not model")
	}
	relay := testharness.Relay(t)
	server := testharness.Host(t, relay, map[uint16]func(net.Conn){
		DirectPort: func(c net.Conn) { c.Close() },
	})
	host := state.Host{Name: "lab", Address: testharness.Address(server)}

	programFiles := t.TempDir()
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", t.TempDir())
	dir := filepath.Join(programFiles, "RustDesk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "RustDesk.exe"), []byte("not run"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(connector.NewPorts(), nil, admission.Dialing{Key: key.NewNode()})

	client, err := c.Connect(host, connector.Config{"port": fmt.Sprint(DirectPort)})
	if err == nil || !strings.Contains(err.Error(), "start RustDesk") {
		t.Fatalf("connect error = %v; want the launch failure", err)
	}
	if client != nil {
		t.Fatal("a failed launch returned a client")
	}
}
