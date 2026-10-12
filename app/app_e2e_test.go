package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// clientRow finds k's row in the gate's lists, as the Settings page shows them.
func clientRow(g *admission.Gate, k key.NodePublic) (admission.Client, bool) {
	for _, st := range []string{admission.Probation, admission.Rejected, admission.Approved, admission.Blocked} {
		for _, c := range g.Clients(st) {
			if c.Key == k {
				return c, true
			}
		}
	}
	return admission.Client{}, false
}

// node is one running Mochi: its app, state directory and the tester that
// drives its window. Work the app does off the UI thread (starting a server,
// sending text) comes back through q, and the test applies it where it can
// step the view, as the real window loop does.
type node struct {
	t      *testing.T
	name   string
	dir    string
	app    *app
	tt     *ui.Tester
	q      chan func()
	opened []string // folders "Open containing folder" asked for
	theme  ui.Theme // the theme the last frame was drawn with

	mu       sync.Mutex
	received map[uint16][]byte // what the server's extra ports were sent
}

// newNode starts a Mochi on dir (a fresh directory if empty) that serves through
// relay and trusts the given machine keys. Its server serves port 9000, a sink
// that records what it is sent, and 21118, an echo, as a host running RustDesk
// with direct IP access would.
func newNode(t *testing.T, name, dir string, relay *tailcfg.DERPRegion, trusted ...ed25519.PublicKey) *node {
	t.Helper()
	if err := registerFonts(); err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		dir = t.TempDir()
	}
	store, err := state.OpenState(filepath.Join(dir, "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetString("ssh_config_path", filepath.Join(dir, "ssh_config")); err != nil {
		t.Fatal(err)
	}
	n := &node{t: t, name: name, dir: dir, q: make(chan func(), 4096), received: map[uint16][]byte{}}
	a := &app{
		relay:    relay,
		runOnUI:  func(fn func()) { n.q <- fn },
		openPath: func(p string) error { n.opened = append(n.opened, p); return nil },
		serveExtra: func(port uint16) func(net.Conn) {
			switch port {
			case 9000:
				return func(c net.Conn) {
					defer c.Close()
					b, _ := io.ReadAll(c)
					n.mu.Lock()
					n.received[port] = append(n.received[port], b...)
					n.mu.Unlock()
				}
			case 21118:
				return func(c net.Conn) { defer c.Close(); io.Copy(c, c) }
			}
			return nil
		},
	}
	if err := a.load(store); err != nil {
		t.Fatal(err)
	}
	a.trusted = append(a.trusted, trusted...)
	n.app = a
	n.tt = ui.NewTester(func(c *ui.Context) {
		n.theme = *c.Theme()
		a.view(c)
	}, uiWidth, uiHeight)
	n.tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 70, Right: 140})
	t.Cleanup(n.shutdown)
	return n
}

// shutdown stops what the node started. Safe to call twice.
func (n *node) shutdown() {
	a := n.app
	if a.stopGate != nil {
		a.stopGate()
		a.stopGate = nil
	}
	if a.server != nil {
		a.server.Close()
		a.server = nil
	}
	if a.store != nil {
		a.store.Close()
		a.store = nil
	}
}

// pump applies the work that came back from other goroutines, then draws.
func (n *node) pump() {
	for {
		select {
		case fn := <-n.q:
			fn()
		default:
			n.tt.Frame()
			return
		}
	}
}

// until pumps the node until cond holds, or fails the test after d.
func (n *node) until(what string, d time.Duration, cond func() bool) {
	n.t.Helper()
	deadline := time.Now().Add(d)
	for {
		n.pump()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			n.t.Fatalf("%s: gave up waiting for %s after %v\nstatus: %q\nvisible: %q", n.name, what, d, n.app.status, n.tt.Texts())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (n *node) click(label string) {
	n.t.Helper()
	revealInView(n.t, n.tt, label)
	if err := n.tt.Click(label); err != nil {
		n.t.Fatalf("%s: %v\nvisible: %q", n.name, err, n.tt.Texts())
	}
	n.pump()
}

// revealInView scrolls the page until the element labeled label is inside the
// window, where a click can reach it. It tries the wheel one way, and the other
// if that moved the element the wrong way.
func revealInView(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	visible := func(r ui.Rect) bool { return r.H > 0 && r.Y >= 44 && r.Y+r.H <= uiHeight-28 }
	r, ok := tt.Find(label)
	if !ok || visible(r) {
		return
	}
	dir := float32(120)
	for i := 0; i < 40 && !visible(r); i++ {
		tt.Scroll(uiWidth/2, uiHeight/2, 0, dir)
		next, ok := tt.Find(label)
		if !ok {
			return
		}
		movedAway := (r.Y > uiHeight && next.Y > r.Y) || (r.Y < 44 && next.Y < r.Y)
		if next.Y == r.Y || movedAway {
			dir = -dir
		}
		r = next
	}
}

func (n *node) typeInto(label, text string) {
	n.t.Helper()
	n.click(label)
	n.tt.Type(text)
	n.pump()
}

func (n *node) has(s string) bool { return n.tt.HasText(s) }

// fill makes the field labeled label hold text: it types into a field that is
// not already right after selecting what is there.
func (n *node) fill(label, text string, get func() string) {
	n.t.Helper()
	n.click(label)
	if get() == text {
		return
	}
	n.tt.Key(ui.Ctrl, ui.KeyA)
	n.tt.Type(text)
	n.pump()
	if get() != text {
		n.t.Fatalf("%s: field %q holds %q, want %q", n.name, label, get(), text)
	}
}

func (n *node) exact(s string) bool {
	for _, t := range n.tt.Texts() {
		if t == s {
			return true
		}
	}
	return false
}

func (n *node) want(texts ...string) {
	n.t.Helper()
	for _, s := range texts {
		if !n.has(s) {
			n.t.Fatalf("%s: expected %q on screen\nstatus: %q\nvisible: %q", n.name, s, n.app.status, n.tt.Texts())
		}
	}
}

func (n *node) wantNot(texts ...string) {
	n.t.Helper()
	for _, s := range texts {
		if n.has(s) {
			n.t.Fatalf("%s: did not expect %q on screen\nvisible: %q", n.name, s, n.tt.Texts())
		}
	}
}

func (n *node) settings() {
	n.t.Helper()
	n.click("⚙  Settings")
	n.want("Machine key")
}

// clientKey is the node key this Mochi dials with, as hosts see it.
func (n *node) clientKey() key.NodePublic { return n.app.identity.NodeKey(state.RoleClient).Public() }

func (n *node) sentTo(port uint16) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return string(n.received[port])
}

// scrollToTop scrolls Settings back to its top, where the server switch is
// whole and clickable, however far the page was scrolled. A negative wheel
// delta scrolls up.
func (n *node) scrollToTop() {
	n.app.pageScroll.ScrollTo(0, 0, ui.Animation{})
	n.until("the page to reach its top", 5*time.Second, func() bool {
		n.pump()
		return !n.app.pageScroll.Scrolling()
	})
}

// toggleServer flips the "Enable Tailcat server" switch, the last thing on its row.
func (n *node) toggleServer() {
	n.t.Helper()
	n.scrollToTop()
	r, ok := n.tt.Find("Enable Tailcat server")
	if !ok {
		n.t.Fatalf("%s: no server switch; visible: %q", n.name, n.tt.Texts())
	}
	n.tt.ClickAt(uiWidth-28-16, r.Y+r.H/2)
	n.pump()
}

// startServer turns the server on from Settings and returns its address.
func (n *node) startServer() string {
	n.t.Helper()
	n.settings()
	if n.app.serverAddr != "" {
		n.t.Fatalf("%s: server already running", n.name)
	}
	n.toggleServer()
	n.until("the server to start", time.Minute, func() bool { return n.app.serverAddr != "" })
	n.want("Server started", "Server address: "+n.app.serverAddr)
	n.click("Copy server address")
	addr := n.tt.Clipboard()
	if addr == "" || addr != n.app.serverAddr {
		n.t.Fatalf("%s: copied server address %q, server is at %q", n.name, addr, n.app.serverAddr)
	}
	if on, _ := n.app.store.BoolSetting("server_enabled"); !on {
		n.t.Fatalf("%s: the server was started but the setting was not saved", n.name)
	}
	return addr
}

// addHost adds a host through the form.
func (n *node) addHost(name, addr string) {
	n.t.Helper()
	n.click("+  Add host")
	n.typeInto("Host name", name)
	n.typeInto("Tailcat address", addr)
	n.click("Save host")
	n.until("the host to be added", 5*time.Second, func() bool { return n.app.status == "Host added" })
	n.want("Launchers")
}

// sendText sends body to port of the selected host through the popup, and
// waits for the outcome.
func (n *node) sendText(port, body string, within time.Duration) string {
	n.t.Helper()
	if !n.exact("Send text to " + n.app.selectedHostName()) { // a failed send leaves the dialog open
		n.click("Send text launcher")
	}
	n.fill("Port", port, func() string { return n.app.sendPort })
	n.fill("Text to send", body, func() string { return n.app.sendBody })
	n.click("Send")
	n.until("the send to finish", within, func() bool {
		return !n.app.sendBusy && !strings.HasPrefix(n.app.status, "Sending")
	})
	return n.app.status
}

func (n *node) enabledTile(label string) bool {
	r, ok := n.tt.Find(label)
	if !ok {
		n.t.Fatalf("%s: no tile %q", n.name, label)
	}
	px := n.tt.Image().RGBAAt(int(r.X)+6, int(r.Y+r.H/2))
	return px.R == n.theme.Accent.R && px.G == n.theme.Accent.G && px.B == n.theme.Accent.B
}

// selectedHostName is the name of the host whose page is open, or "".
func (a *app) selectedHostName() string {
	for _, h := range a.hosts {
		if h.Address == a.selectedHost {
			return h.Name
		}
	}
	return ""
}

// TestApplication runs Mochi the way people do: one app serves, others connect
// and are admitted by an administrator, by a certificate, or not at all, and
// every launcher and setting gets used. Each step sees the effects of the ones
// before it, as a session does.
func TestApplication(t *testing.T) {
	relay := testharness.Relay(t)

	// An organization CA: a machine key the host's build trusts.
	caPub, caPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	host := newNode(t, "host", "", relay, caPub)
	alice := newNode(t, "alice", "", relay)
	bob := newNode(t, "bob", "", relay)

	var addr string
	step := func(name string, fn func(t *testing.T)) {
		if !t.Run(name, fn) {
			t.FailNow() // later steps depend on this one
		}
	}

	step("host turns its server on from Settings", func(t *testing.T) {
		addr = host.startServer()
		if _, err := tailcat.ParseAddr(tailcat.Addr(addr)); err != nil {
			t.Fatalf("the server address does not parse: %v", err)
		}
		host.want("Pending approval", "Approved", "Blocked")
		// The database folder can be opened from Settings.
		host.click("Open containing folder")
		if len(host.opened) != 1 || host.opened[0] != host.dir {
			t.Fatalf("opened %q, want the state folder %q", host.opened, host.dir)
		}
	})

	step("alice adds the host from its address", func(t *testing.T) {
		alice.addHost("lab", addr)
		stored, _ := alice.app.store.Hosts()
		if len(stored) != 1 || stored[0] != (state.Host{Name: "lab", Address: addr}) {
			t.Fatalf("stored hosts = %+v", stored)
		}
		alice.want("Copy connection string", "Copy node ID")
		alice.click("Copy connection string")
		if alice.tt.Clipboard() != addr {
			t.Fatal("the copied connection string is not the address")
		}
	})

	step("an unapproved client is told why, and the host admin sees it", func(t *testing.T) {
		status := alice.sendText("9000", "hello", time.Minute)
		if !strings.Contains(status, "pending approval") || !strings.HasPrefix(status, "Send failed") {
			t.Fatalf("status = %q", status)
		}
		if got := host.sentTo(9000); got != "" {
			t.Fatalf("an unapproved client's text arrived: %q", got)
		}
		host.until("alice to be listed as pending", 20*time.Second, func() bool { return host.has(alice.clientKey().String()) })
		host.want("no certificate")
	})

	step("the admin approves it from the list", func(t *testing.T) {
		host.click("Approve")
		host.until("approval", 5*time.Second, func() bool { return strings.HasPrefix(host.app.status, "Approved ") })
		host.want(alice.clientKey().String(), "this machine, until")
		host.wantNot("no certificate")
		if r, ok := clientRow(host.app.gate, alice.clientKey()); !ok || r.State != admission.Approved || r.CA != host.app.gate.SelfFingerprint() {
			t.Fatalf("row = %+v", r)
		}
	})

	step("the approved client sends text that arrives", func(t *testing.T) {
		status := alice.sendText("9000", "hello", time.Minute)
		if status != "Sent 5 bytes to lab port 9000" {
			t.Fatalf("status = %q", status)
		}
		alice.until("the dialog to close", 5*time.Second, func() bool { return !alice.exact("Send text to lab") })
		host.until("the text to arrive", 10*time.Second, func() bool { return host.sentTo(9000) == "hello" })
	})

	step("the SSH launcher writes and removes the client's ssh config", func(t *testing.T) {
		cfg := filepath.Join(alice.dir, "ssh_config")
		alice.click("SSH launcher")
		if alice.app.status != "SSH enabled" {
			t.Fatalf("status = %q", alice.app.status)
		}
		raw, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"# BEGIN MOCHI MANAGED", "Host lab", "HostName " + addr, "connector-ssh-proxy " + addr + " %p"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("ssh config lacks %q:\n%s", want, raw)
			}
		}
		if !alice.enabledTile("SSH launcher") {
			t.Fatal("the enabled SSH tile is not accent-filled")
		}
		alice.click("SSH launcher")
		raw, _ = os.ReadFile(cfg)
		if strings.Contains(string(raw), "MOCHI MANAGED") || alice.app.status != "SSH disabled" {
			t.Fatalf("status %q; config after disabling:\n%s", alice.app.status, raw)
		}
		if alice.enabledTile("SSH launcher") {
			t.Fatal("a disabled SSH tile is still accent-filled")
		}
	})

	step("the host's shell service answers an approved client", func(t *testing.T) {
		d := alice.app.dialing.Dialer(tailcat.Addr(addr))
		defer d.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		conn, err := d.DialTCPPort(ctx, 22)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		buf := make([]byte, 4)
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "SSH-" {
			t.Fatalf("port 22 sent %q, err %v; want an SSH banner", buf, err)
		}
	})

	step("the RustDesk launcher forwards the host's direct-IP port and starts the client", func(t *testing.T) {
		argsFile := testharness.FakeRustDesk(t)
		alice.click("RustDesk launcher")
		alice.until("the forward to start", time.Minute, func() bool {
			return strings.HasPrefix(alice.app.status, "RustDesk address copied") || strings.Contains(alice.app.status, "failed")
		})
		local := alice.tt.Clipboard()
		var launched []string
		alice.until("RustDesk to start", time.Minute, func() bool {
			raw, err := os.ReadFile(argsFile)
			launched = strings.Fields(string(raw))
			return err == nil && len(launched) == 2
		})
		if launched[0] != "--connect" || launched[1] != local || !strings.HasPrefix(local, "127.0.0.1:") {
			t.Fatalf("launched %q; clipboard %q; status %q", launched, local, alice.app.status)
		}
		if !alice.enabledTile("RustDesk launcher") {
			t.Fatal("the enabled RustDesk tile is not accent-filled")
		}
		// The forward really reaches the host.
		conn, err := net.DialTimeout("tcp", local, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte("ping"))
		got := make([]byte, 4)
		conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
			t.Fatalf("through the forward got %q, err %v", got, err)
		}
		conn.Close()

		alice.click("RustDesk launcher")
		if alice.app.status != "RustDesk disabled" {
			t.Fatalf("status = %q", alice.app.status)
		}
		if c, err := net.DialTimeout("tcp", local, 2*time.Second); err == nil {
			c.Close()
			t.Fatal("the forward is still open after disabling")
		}
	})

	step("a certificate from a trusted key admits bob without the admin", func(t *testing.T) {
		bob.addHost("lab", addr)
		cert, err := admission.Issue(caPriv, bob.clientKey(), "bob@example.com", time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		bob.settings()
		bob.typeInto("Certificate", hex.EncodeToString(cert))
		bob.click("Add")
		if bob.app.status != "Certificate saved" {
			t.Fatalf("status = %q", bob.app.status)
		}
		bob.click("lab")
		status := bob.sendText("9000", "from bob", time.Minute)
		if status != "Sent 8 bytes to lab port 9000" {
			t.Fatalf("status = %q", status)
		}
		host.until("bob's text", 10*time.Second, func() bool { return strings.Contains(host.sentTo(9000), "from bob") })
		host.settings()
		host.until("bob to be listed", 10*time.Second, func() bool { return host.has("bob@example.com, until") })
		host.want(bob.clientKey().String())
	})

	step("a plain tailcat client waits in the list and is admitted by the admin", func(t *testing.T) {
		k := key.NewNode()
		plain := &tailcat.Client{Server: tailcat.Addr(addr), Key: k}
		t.Cleanup(func() { plain.Close() })
		dial := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			conn, err := plain.DialTCPPort(ctx, 9000)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = conn.Write([]byte("plain"))
			return err
		}
		_ = dial() // refused: it has no way to prove itself
		host.until("the plain client to be listed", 20*time.Second, func() bool { return host.has(k.Public().String()) })
		if strings.Contains(host.sentTo(9000), "plain") {
			t.Fatal("an unapproved plain client reached a service")
		}
		host.click("Approve " + k.Public().String())
		host.until("approval", 5*time.Second, func() bool {
			r, ok := clientRow(host.app.gate, k.Public())
			return ok && r.State == admission.Approved
		})
		plain.Close()
		plain = &tailcat.Client{Server: tailcat.Addr(addr), Key: k}
		if err := dial(); err != nil {
			t.Fatalf("an approved plain client could not connect: %v", err)
		}
		host.until("the plain text", 10*time.Second, func() bool { return strings.Contains(host.sentTo(9000), "plain") })
	})

	step("blocking a client from the list cuts it off", func(t *testing.T) {
		host.settings()
		host.click("Block " + alice.clientKey().String())
		host.until("the block", 5*time.Second, func() bool { return strings.HasPrefix(host.app.status, "Blocked ") })
		if r, _ := clientRow(host.app.gate, alice.clientKey()); r.State != admission.Blocked {
			t.Fatal("alice is not blocked")
		}
		host.want("Blocked")
		// A blocked node gets no tunnel at all: its dial times out.
		d := alice.app.dialing.Dialer(tailcat.Addr(addr))
		defer d.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if conn, err := d.DialTCPPort(ctx, 9000); err == nil {
			conn.Close()
			t.Fatal("a blocked client connected")
		}
		// Bob, approved by a certificate, is unaffected.
		if r, _ := clientRow(host.app.gate, bob.clientKey()); r.State != admission.Approved {
			t.Fatalf("bob's state = %q", r.State)
		}
	})

	step("the admin lifts the block and alice is back", func(t *testing.T) {
		host.click("Approve " + alice.clientKey().String())
		host.until("the approval", 5*time.Second, func() bool {
			r, ok := clientRow(host.app.gate, alice.clientKey())
			return ok && r.State == admission.Approved
		})
		alice.click("lab")
		status := alice.sendText("9000", "back again", time.Minute)
		if !strings.HasPrefix(status, "Sent ") {
			t.Fatalf("status = %q", status)
		}
	})

	step("removing a host disables its launchers and forgets it", func(t *testing.T) {
		alice.click("SSH launcher")
		cfg := filepath.Join(alice.dir, "ssh_config")
		if raw, _ := os.ReadFile(cfg); !strings.Contains(string(raw), "MOCHI MANAGED") {
			t.Fatalf("SSH was not enabled: %q (status %q)", raw, alice.app.status)
		}
		alice.click("Remove host")
		alice.want("Remove lab?")
		alice.click("No")
		alice.wantNot("Remove lab?")
		alice.click("Remove host")
		alice.click("Yes")
		if raw, _ := os.ReadFile(cfg); strings.Contains(string(raw), "MOCHI MANAGED") {
			t.Fatalf("the ssh config entry survived removal:\n%s", raw)
		}
		if stored, _ := alice.app.store.Hosts(); len(stored) != 0 {
			t.Fatalf("stored hosts = %+v", stored)
		}
		alice.want("Host removed", "Select a host from the sidebar")
	})

	step("the host stops its server from Settings", func(t *testing.T) {
		host.settings()
		host.scrollToTop()
		host.toggleServer()
		host.until("the server to stop", 30*time.Second, func() bool { return host.app.status == "Server stopped" })
		if host.app.serverAddr != "" {
			t.Fatal("the server stopped but still has an address")
		}
		host.wantNot("Server address:", "Pending approval", "Approved")
		if on, _ := host.app.store.BoolSetting("server_enabled"); on {
			t.Fatal("the server is stopped but the setting still says on")
		}
	})

	step("a restarted host keeps its address and who it approved", func(t *testing.T) {
		dir := host.dir
		host.shutdown()
		again := newNode(t, "host again", dir, relay, caPub)
		if again.app.serverEnabled {
			t.Fatal("the restarted app thinks the server is enabled")
		}
		again.settings()
		again.toggleServer()
		again.until("the server to start again", time.Minute, func() bool { return again.app.serverAddr != "" })
		if again.app.serverAddr != addr {
			t.Fatalf("the address changed across a restart:\n  before %s\n  after  %s", addr, again.app.serverAddr)
		}
		// Bob was approved before the restart and sends without presenting anything new.
		bob.app.store.DB().Exec(`DELETE FROM certs`)
		bob.click("lab")
		status := bob.sendText("9000", "after restart", time.Minute)
		if !strings.HasPrefix(status, "Sent ") {
			t.Fatalf("status = %q", status)
		}
		again.until("bob's text", 10*time.Second, func() bool { return strings.Contains(again.sentTo(9000), "after restart") })
	})
}
