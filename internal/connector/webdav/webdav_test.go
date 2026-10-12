package webdav

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/share"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// newRoot returns a directory that holds a.txt.
func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func doHTTP(t *testing.T, method, target, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// checkShare drives a share at base the way Explorer does: reads, a listing, a
// write, and a move whose Destination is the new path.
func checkShare(t *testing.T, base, root string) {
	t.Helper()
	if code, body := doHTTP(t, "GET", base+"a.txt", "", nil); code != 200 || body != "hi" {
		t.Fatalf("GET a.txt = %d %q; want 200 \"hi\"", code, body)
	}
	if code, body := doHTTP(t, "PROPFIND", base, "", map[string]string{"Depth": "1"}); code != 207 || !strings.Contains(body, "/a.txt") {
		t.Fatalf("PROPFIND = %d %s; want 207 listing a.txt", code, body)
	}
	if code, _ := doHTTP(t, "PUT", base+"b.txt", "new", nil); code != 201 && code != 204 {
		t.Fatalf("PUT = %d", code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(got) != "new" {
		t.Fatalf("host file = %q; want \"new\"", got)
	}
	if code, _ := doHTTP(t, "MOVE", base+"b.txt", "", map[string]string{"Destination": base + "c.txt"}); code != 201 && code != 204 {
		t.Fatalf("MOVE = %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("MOVE left the source in place")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "c.txt")); string(got) != "new" {
		t.Fatalf("moved file = %q; want \"new\"", got)
	}
}

// A share over a real Tailcat tunnel reaches the host's root: reads, listings,
// writes and moves all land on the host's disk.
func TestShareOverTailcatTunnel(t *testing.T) {
	root := newRoot(t)
	hostPorts := connector.NewPorts()
	host := newConnector(hostPorts, nil, nil, admission.Dialing{}, root, t.Logf)
	if err := host.Listen(); err != nil {
		t.Fatal(err)
	}
	server := testharness.HostFunc(t, testharness.Relay(t), hostPorts.Handler)

	client := admission.Dialing{Key: key.NewNode()}.Dialer(tailcat.Addr(testharness.Address(server)))
	tun, err := connector.OpenTunnel(client, ServePort)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tun.Disconnect() })
	checkShare(t, "http://127.0.0.1:"+strconv.Itoa(tun.Port())+"/", root)
}

// Connect mounts the share and copies its address. Only Linux runs its file
// manager step as a no-op here; Windows and macOS would open a real window.
func TestConnectCopiesTheShareAddress(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Connect opens the platform file manager, which only Linux can run here")
	}
	root := newRoot(t)
	hostPorts := connector.NewPorts()
	host := newConnector(hostPorts, nil, nil, admission.Dialing{}, root, t.Logf)
	if err := host.Listen(); err != nil {
		t.Fatal(err)
	}
	server := testharness.HostFunc(t, testharness.Relay(t), hostPorts.Handler)

	var copied string
	c := newConnector(connector.NewPorts(), func(s string) { copied = s }, nil, admission.Dialing{Key: key.NewNode()}, "", t.Logf)
	client, err := c.Connect(state.Host{Name: "lab", Address: testharness.Address(server)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { client.Disconnect() })
	port := client.(*connector.Tunnel).Port()
	if want := share.Address(runtime.GOOS, port); copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	checkShare(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/", root)
}

// The server side serves only between Listen and Stop, and it needs a home
// volume to serve.
func TestListenServesOnlyWhileOn(t *testing.T) {
	ports := connector.NewPorts()
	if err := newConnector(ports, nil, nil, admission.Dialing{}, "", nil).Listen(); err == nil {
		t.Fatal("Listen succeeded with no home volume to serve")
	}

	c := newConnector(ports, nil, nil, admission.Dialing{}, newRoot(t), t.Logf)
	access := true
	c.access = connector.NewDiskAccess(func() bool { return access }, func() {})
	if c.Healthy() {
		t.Fatal("healthy before Listen")
	}
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	if !ports.Registered(ServePort) || !c.Healthy() {
		t.Fatal("WebDAV port not served after Listen")
	}
	access = false
	if c.Healthy() {
		t.Fatal("healthy without access to every folder it serves")
	}
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if ports.Registered(ServePort) || c.Healthy() {
		t.Fatal("WebDAV port served after Stop")
	}
}
