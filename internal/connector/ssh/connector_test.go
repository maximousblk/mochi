package ssh

import (
	"fmt"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/sshserver"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	gossh "golang.org/x/crypto/ssh"
	"tailscale.com/types/key"
)

// Listen serves SSH on ServePort from Listen until Stop.
func TestListenServesSSHUntilStop(t *testing.T) {
	ports := connector.NewPorts()
	c := New(ports, state.NewIdentity().Seed(), nil, nil, admission.Dialing{})
	access := true
	c.access = connector.NewDiskAccess(func() bool { return access }, func() {})
	if c.Healthy() {
		t.Fatal("healthy before Listen")
	}
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	if !ports.Registered(ServePort) || !c.Healthy() {
		t.Fatal("SSH port not served after Listen")
	}
	access = false
	if c.Healthy() {
		t.Fatal("healthy while its shells cannot reach the user's files")
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if ports.Registered(ServePort) || c.Healthy() {
		t.Fatal("SSH port served after Stop")
	}
}

// Through the tunnel the copied command names, a client reaches the host's SSH
// server with the current account's user name and no credentials. The host
// presents the key derived from its identity, and the account runs commands.
func TestSSHOverTailcatThroughCopiedCommand(t *testing.T) {
	hostSeed := state.NewIdentity().Seed()
	hostPorts := connector.NewPorts()
	host := New(hostPorts, hostSeed, nil, nil, admission.Dialing{})
	if err := host.Listen(); err != nil {
		t.Fatal(err)
	}
	server := testharness.HostFunc(t, testharness.Relay(t), hostPorts.Handler)
	address := testharness.Address(server)

	var copied string
	client := New(connector.NewPorts(), nil, func(s string) { copied = s }, nil, admission.Dialing{Key: key.NewNode()})
	c, err := client.Connect(state.Host{Name: "lab", Address: address})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Disconnect() })
	port := c.(*connector.Tunnel).Port()

	info, err := connector.ParseHost(address)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("ssh -p %d -o HostKeyAlias=mochi-node-%s 127.0.0.1", port, info.ServerPublic.NodePublic.UntypedHexString())
	if copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}

	hostKey, err := sshserver.HostKey(hostSeed)
	if err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := gossh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &gossh.ClientConfig{
		User:            me.Username,
		HostKeyCallback: gossh.FixedHostKey(hostKey.PublicKey()),
		Timeout:         30 * time.Second,
	})
	if err != nil {
		t.Fatalf("ssh dial through the tunnel: %v", err)
	}
	defer conn.Close()
	sess, err := conn.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.Output("echo hi")
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hi" {
		t.Fatalf("command output = %q, want \"hi\"", got)
	}
}
