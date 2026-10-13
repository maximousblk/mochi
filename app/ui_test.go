package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"image/color"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/connector/textsender"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

const (
	uiWidth  = 1100
	uiHeight = 700
)

// toggleConnector is a launcher whose connections the test controls. Connect
// fails with enableErr, or hands back a fakeClient that records its disconnect.
// When hold is set, Connect blocks until hold is closed, as a slow dial does.
// Each client closes gone when it disconnects, when gone is set.
type toggleConnector struct {
	name      string
	enableErr error
	hold      chan struct{}
	gone      chan struct{}
	connects  int
	clients   []*fakeClient
}

func (c *toggleConnector) Name() string  { return c.name }
func (c *toggleConnector) Icon() *ui.SVG { return webdavIcon }
func (c *toggleConnector) ServerFields() []connector.Field {
	return []connector.Field{{Key: "port", Label: "Tailcat port", Default: "9000", Check: connector.CheckPort}}
}
func (c *toggleConnector) ClientFields() []connector.Field {
	return []connector.Field{{Key: "port", Label: "Host's port", Default: "9000", Check: connector.CheckPort}}
}
func (c *toggleConnector) Connect(_ state.Host, _ connector.Config, _ func(error)) (connector.Client, error) {
	c.connects++
	if c.hold != nil {
		<-c.hold
	}
	if c.enableErr != nil {
		return nil, c.enableErr
	}
	fc := &fakeClient{last: time.Now(), gone: c.gone}
	c.clients = append(c.clients, fc)
	return fc, nil
}
func (c *toggleConnector) Listen(connector.Config) error { return nil }
func (c *toggleConnector) Stop() error                   { return nil }
func (c *toggleConnector) Healthy() bool                 { return true }

// uiEnv is the app under test with its state in a temporary database, and the
// tester that drives its window. Work the app runs off the UI thread comes back
// through q, and the test runs it where until and idle step the view.
type uiEnv struct {
	t       *testing.T
	app     *app
	tt      *ui.Tester
	gate    *admission.Gate
	theme   ui.Theme
	q       chan func()
	windows windowSet
}

// fakeWindow stands in for a dialog's native window. It draws the dialog's view
// in its own tester, so a test clicks a dialog as it clicks the main window.
type fakeWindow struct {
	title    string
	tt       *ui.Tester
	closed   bool
	onClosed func()
}

func (w *fakeWindow) Update(fn func()) { fn() }
func (w *fakeWindow) Invalidate()      {}
func (w *fakeWindow) Focus()           {}
func (w *fakeWindow) OnClosed(fn func()) func() {
	w.onClosed = fn
	return func() { w.onClosed = nil }
}
func (w *fakeWindow) Close() {
	if w.closed {
		return
	}
	w.closed = true
	if w.onClosed != nil {
		w.onClosed()
	}
}

// windowSet records the dialog windows a test opens in place of native ones.
type windowSet struct{ all []*fakeWindow }

func (s *windowSet) open(title string, w, h int, view func(*ui.Context)) dialogWindow {
	win := &fakeWindow{title: title, tt: ui.NewTester(view, w, h)}
	s.all = append(s.all, win)
	return win
}

// find returns the most recently opened window titled title, open or closed,
// or nil if none was ever opened.
func (s *windowSet) find(title string) *fakeWindow {
	for i := len(s.all) - 1; i >= 0; i-- {
		if s.all[i].title == title {
			return s.all[i]
		}
	}
	return nil
}

// openCount is how many dialog windows are open now.
func (s *windowSet) openCount() int {
	n := 0
	for _, w := range s.all {
		if !w.closed {
			n++
		}
	}
	return n
}

func testAddress(t *testing.T) string {
	t.Helper()
	ci := tailcat.ConnInfo{
		ServerPublic: tailcat.NodePublic{NodePublic: key.NewNode().Public()},
		PresharedKey: tailcat.NewPresharedKey(),
		RegionID:     1,
	}
	return string(ci.Addr())
}

// newUI builds an app with a real store and gate and renders it. withGate
// leaves a.gate nil when false, as when the server is stopped.
func newUI(t *testing.T, withGate bool, hosts []state.Host, connectors ...connector.Connector) *uiEnv {
	t.Helper()
	if err := registerFonts(); err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	id, err := store.Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if err := store.AddHost(h); err != nil {
			t.Fatal(err)
		}
	}
	e := &uiEnv{t: t, q: make(chan func(), 4096)}
	a := &app{
		hosts:       hosts,
		store:       store,
		identity:    id,
		ports:       connector.NewPorts(),
		serving:     map[string]bool{},
		connections: map[string]map[string]connector.Client{},
		dialing: admission.Dialing{Key: id.NodeKey(state.RoleClient), Certs: func() [][]byte {
			certs, _ := store.Certs()
			return certs
		}},
		connectors: connectors,
		runOnUI:    func(fn func()) { e.q <- fn },
		openWindow: e.windows.open,
	}
	for _, c := range connectors {
		a.servers = append(a.servers, c)
	}
	a.servers = append(a.servers, textsender.NewReceiver(a.ports, textSenderIcon, a.canNotify, a.receiveText))
	e.app = a
	if withGate {
		extPub, _, _ := ed25519.GenerateKey(nil)
		g, err := admission.NewGate(store.DB(), id.Private(), []ed25519.PublicKey{extPub})
		if err != nil {
			t.Fatal(err)
		}
		// Installed on a server that is never started: the handler hooks run only
		// on connections, and DisconnectClient is a no-op without a server.
		g.Install(&tailcat.Server{}, nil)
		a.gate, e.gate = g, g
	}
	e.tt = ui.NewTester(func(c *ui.Context) {
		e.theme = *c.Theme()
		a.view(c)
	}, uiWidth, uiHeight)
	e.tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 70, Right: 140})
	return e
}

// until runs the work that background goroutines queued, as the window's loop
// does, until cond holds. It fails the test if cond does not hold in five seconds.
func (e *uiEnv) until(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		select {
		case fn := <-e.q:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// idle waits until no operation is running and no list read is pending, then
// draws the view. Each operation clears its busy state in the same UI step that
// applies its result, and a Block or Approve starts a list read after it.
func (e *uiEnv) idle() {
	e.t.Helper()
	e.until("the running operations", func() bool {
		for _, busy := range e.app.busy {
			if busy {
				return false
			}
		}
		return !e.app.clientsReload.busy() && !e.app.inboxReload.busy()
	})
	e.tt.Frame()
}

// settle waits until the operation op is no longer busy, then draws the view. A
// test uses it after a click that starts an operation, such as a Connect, which
// runs off the UI thread and applies its result on the UI thread later.
func (e *uiEnv) settle(op string) {
	e.t.Helper()
	e.until("the operation "+op, func() bool { return !e.app.busy[op] })
	e.tt.Frame()
}

// dialog returns a tester for the dialog window titled title: a copy of e that
// clicks and reads that window. It fails the test when no such window is open.
func (e *uiEnv) dialog(title string) *uiEnv {
	e.t.Helper()
	if !e.dialogOpen(title) {
		e.t.Fatalf("no open dialog %q; main window shows %q", title, e.tt.Texts())
	}
	d := *e
	d.tt = e.windows.find(title).tt
	return &d
}

func (e *uiEnv) dialogOpen(title string) bool {
	w := e.windows.find(title)
	return w != nil && !w.closed
}

func (e *uiEnv) click(label string) {
	e.t.Helper()
	revealInView(e.t, e.tt, label)
	if err := e.tt.Click(label); err != nil {
		e.t.Fatalf("%v\nvisible: %q", err, e.tt.Texts())
	}
}

func (e *uiEnv) typeInto(label, text string) {
	e.t.Helper()
	e.click(label)
	e.tt.Type(text)
}

// replace makes the field labeled label hold exactly text.
func (e *uiEnv) replace(label, text string) {
	e.t.Helper()
	e.click(label)
	e.tt.Key(ui.Ctrl, ui.KeyA)
	e.tt.Type(text)
}

func (e *uiEnv) want(texts ...string) {
	e.t.Helper()
	for _, s := range texts {
		if !e.tt.HasText(s) {
			e.t.Fatalf("expected %q on screen; visible: %q", s, e.tt.Texts())
		}
	}
}

// shows reports whether some element's text is exactly s (want matches a part).
func (e *uiEnv) shows(s string) bool {
	for _, t := range e.tt.Texts() {
		if t == s {
			return true
		}
	}
	return false
}

func (e *uiEnv) wantNot(texts ...string) {
	e.t.Helper()
	for _, s := range texts {
		if e.tt.HasText(s) {
			e.t.Fatalf("did not expect %q on screen; visible: %q", s, e.tt.Texts())
		}
	}
}

// refresh does what the app's frames do on the self page: they start a read of
// the client lists, which lands a little later. It waits for the read.
func (e *uiEnv) refresh() {
	e.t.Helper()
	e.until("the client lists", func() bool { return !e.app.clientsReload.busy() })
	e.app.reloadClients()
	e.until("the client lists", func() bool { return !e.app.clientsReload.busy() })
	e.tt.Frame()
}

func (e *uiEnv) status() string { return e.app.status }

// openSettings opens the Settings page from the gear in the title bar.
func (e *uiEnv) openSettings() {
	e.t.Helper()
	e.click("Settings")
	e.want("Database")
}

func (e *uiEnv) pixel(x, y float32) color.RGBA { return e.tt.Image().RGBAAt(int(x), int(y)) }

func sameColor(c color.RGBA, w ui.Color) bool { return c.R == w.R && c.G == w.G && c.B == w.B }

func TestUIEmptyState(t *testing.T) {
	e := newUI(t, false, nil)
	e.want("Mochi", "Hosts", "+  Add host", selfTitle())
	e.wantNot("Remove host")
}

// The layout the sidebar, title bar and status bar were built to have: the
// title bar spans the full width above everything, the sidebar runs from it to
// the bottom, and the status bar sits under the content only.
func TestUILayout(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []state.Host{h})
	e.app.status = "Everything is fine"
	e.tt.Frame()

	title, _ := e.tt.Find("Mochi")
	if title.Y > 44 {
		t.Errorf("title is at y=%v, below the 44px title bar", title.Y)
	}
	if title.X < 70 {
		t.Errorf("title starts at x=%v, under the native window controls (70 reserved)", title.X)
	}
	side, _ := e.tt.Find("Settings")
	if side.X < 240 {
		t.Errorf("Settings button at x=%v, not in the title bar's right corner", side.X)
	}
	if side.Y > 44 {
		t.Errorf("Settings button at y=%v, below the 44px title bar", side.Y)
	}
	status, _ := e.tt.Find("Everything is fine")
	if status.X < 240 {
		t.Errorf("status text at x=%v, under the sidebar: it must sit right of it", status.X)
	}
	if status.Y < uiHeight-28 {
		t.Errorf("status text at y=%v, not in the bottom bar", status.Y)
	}
	// The sidebar surface reaches the bottom-left corner; the status bar is
	// not drawn there.
	if got := e.pixel(10, uiHeight-3); !sameColor(got, e.theme.Surface) {
		t.Errorf("bottom-left pixel = %v, want the sidebar surface %v", got, e.theme.Surface)
	}
	// Dark mode must lay out the same and not panic.
	e.tt.SetDark(true)
	e.want("Mochi", "lab")
}

func TestUIAddHost(t *testing.T) {
	e := newUI(t, false, nil)
	addr := testAddress(t)

	e.click("+  Add host")
	e.want("Save host", "Cancel")
	if !e.shows("Add host") {
		t.Fatalf("no Add host heading; visible: %q", e.tt.Texts())
	}

	// Missing fields.
	e.click("Save host")
	if got := e.status(); got != "Host name and address are required" {
		t.Fatalf("status = %q", got)
	}
	if !e.shows("Add host") {
		t.Fatal("left the form after a failed save")
	}

	// A bad address is refused with the reason, and nothing is saved.
	e.typeInto("Host name", "lab")
	e.typeInto("Tailcat address", "not an address")
	e.click("Save host")
	if !strings.HasPrefix(e.status(), "Invalid Tailcat address") {
		t.Fatalf("status = %q", e.status())
	}
	if hosts, _ := e.app.store.Hosts(); len(hosts) != 0 {
		t.Fatalf("a bad address was saved: %+v", hosts)
	}

	// Cancel clears the drafts and leaves the form.
	e.click("Cancel")
	if e.shows("Add host") {
		t.Fatal("still on the form after Cancel")
	}
	if e.app.nameDraft != "" || e.app.addrDraft != "" {
		t.Fatalf("drafts survived Cancel: %q %q", e.app.nameDraft, e.app.addrDraft)
	}

	// A good one is saved, selected and shown. The write runs off the UI thread.
	e.click("+  Add host")
	e.typeInto("Host name", "lab")
	e.typeInto("Tailcat address", addr)
	e.click("Save host")
	e.idle()
	if e.status() != "Host added" {
		t.Fatalf("status = %q", e.status())
	}
	hosts, _ := e.app.store.Hosts()
	if len(hosts) != 1 || hosts[0] != (state.Host{Name: "lab", Address: addr}) {
		t.Fatalf("saved hosts = %+v", hosts)
	}
	e.want("Launchers", "Host added")
	if e.shows("Add host") {
		t.Fatal("still on the form after saving")
	}
}

func TestUISelectHostAndShowDetails(t *testing.T) {
	h1 := state.Host{Name: "alpha", Address: testAddress(t)}
	h2 := state.Host{Name: "beta", Address: testAddress(t)}
	e := newUI(t, false, []state.Host{h1, h2})
	e.want("alpha", "beta", selfTitle())

	e.click("beta")
	if want := (page{kind: pageHost, address: h2.Address}); e.app.page != want {
		t.Fatalf("page = %+v, want beta's host page", e.app.page)
	}
	// The sidebar lists every host's address, so the page is told apart by
	// the node ID it shows.
	e.want("Launchers", nodeIDFromAddress(h2.Address), "Copy connection string", "Copy node ID")
	e.wantNot(nodeIDFromAddress(h1.Address))

	e.click("alpha")
	e.want(nodeIDFromAddress(h1.Address))
	e.wantNot(nodeIDFromAddress(h2.Address))

	// Settings replaces the host page, and selecting a host brings it back.
	e.openSettings()
	e.wantNot("Launchers")
	e.click("beta")
	e.want("Launchers")
	e.wantNot("Machine key")
}

func TestUICopyButtons(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []state.Host{h})
	e.click("lab")

	e.click("Copy connection string")
	if got := e.tt.Clipboard(); got != h.Address {
		t.Fatalf("clipboard = %q, want the address", got)
	}
	if e.status() != "Copied connection string" {
		t.Fatalf("status = %q", e.status())
	}
	e.want("Copied connection string")

	e.click("Copy node ID")
	if got := e.tt.Clipboard(); got != nodeIDFromAddress(h.Address) || got == "" {
		t.Fatalf("clipboard = %q, want the node ID", got)
	}
}

func TestUILaunchers(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	plain := &toggleConnector{name: "Plain"}
	e := newUI(t, false, []state.Host{h}, plain)
	e.click("lab")
	e.want("Plain", "Send text")

	tile, ok := e.tt.Find("Plain launcher")
	if !ok {
		t.Fatalf("no Plain tile; visible: %q", e.tt.Texts())
	}
	probe := func() color.RGBA { return e.pixel(tile.X+6, tile.Y+tile.H/2) }
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a disabled launcher is drawn in the accent color")
	}

	// Connecting: the launcher is called, and its tile turns accent-filled.
	e.click("Plain launcher")
	e.settle(connectOp(h.Address, "Plain"))
	if plain.connects != 1 || len(e.app.connections[h.Address]) != 1 {
		t.Fatalf("connects=%d connections=%v", plain.connects, e.app.connections)
	}
	if e.status() != "Plain connected" {
		t.Fatalf("status = %q", e.status())
	}
	if !sameColor(probe(), e.theme.Accent) {
		t.Fatalf("an enabled launcher is %v, want the accent color %v", probe(), e.theme.Accent)
	}

	// Disconnecting disconnects the client, and returns the tile to plain.
	e.click("Plain launcher")
	e.settle(connectOp(h.Address, "Plain"))
	if got := plain.clients[0].disconnects; got != 1 || len(e.app.connections) != 0 || e.status() != "Plain disconnected" {
		t.Fatalf("disconnects=%d connections=%v status=%q", got, e.app.connections, e.status())
	}
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a disabled launcher stayed accent-filled")
	}

	// A failure is shown, and the tile stays plain.
	plain.enableErr = errors.New("no route to host")
	e.click("Plain launcher")
	e.settle(connectOp(h.Address, "Plain"))
	if e.status() != "Plain connector failed: no route to host" {
		t.Fatalf("status = %q", e.status())
	}
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a failed connect left the tile accent-filled")
	}
}

func TestUIRemoveHost(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	other := state.Host{Name: "keep", Address: testAddress(t)}
	c := &toggleConnector{name: "Plain"}
	e := newUI(t, false, []state.Host{h, other}, c)
	e.click("lab")
	e.click("Plain launcher")
	e.idle()

	e.click("Remove host")
	d := e.dialog("Remove lab?")
	d.want("This removes the host and closes its connections.", "Yes", "No")

	// No keeps everything, and closes the window.
	d.click("No")
	if e.dialogOpen("Remove lab?") {
		t.Fatal("the Remove window stayed open after No")
	}
	if len(e.app.hosts) != 2 || c.clients[0].disconnects != 0 {
		t.Fatalf("hosts=%d disconnects=%d after No", len(e.app.hosts), c.clients[0].disconnects)
	}

	// Yes disconnects the host's clients, deletes the host from memory and the
	// store, closes the window, and falls back to the empty page.
	e.click("Remove host")
	e.dialog("Remove lab?").click("Yes")
	e.idle()
	if c.clients[0].disconnects != 1 {
		t.Fatalf("client disconnected %d times, want once", c.clients[0].disconnects)
	}
	if len(e.app.hosts) != 1 || e.app.hosts[0] != other {
		t.Fatalf("hosts = %+v", e.app.hosts)
	}
	stored, _ := e.app.store.Hosts()
	if len(stored) != 1 || stored[0] != other {
		t.Fatalf("stored hosts = %+v", stored)
	}
	if e.dialogOpen("Remove lab?") {
		t.Fatal("the Remove window stayed open after Yes")
	}
	e.want("keep", "Host removed")
	e.wantNot("Remove lab?", h.Address, nodeIDFromAddress(h.Address))
}

func TestUISendTextDialog(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []state.Host{h})
	e.click("lab")

	e.click("Send text launcher")
	e.until("the Send text window", func() bool { return e.dialogOpen("Send text to lab") })
	d := e.dialog("Send text to lab")
	d.want("Send", "Cancel")

	// A bad port keeps the window open, and nothing is sent.
	d.replace("Port", "70000")
	d.replace("Text to send", "hello")
	d.click("Send")
	if !e.dialogOpen("Send text to lab") {
		t.Fatal("a bad port closed the window")
	}
	if e.app.busy[sendKey(h.Address)] {
		t.Fatal("a bad port started a send")
	}

	// An empty body is refused with the reason.
	d.replace("Port", "")
	d.replace("Text to send", "")
	d.click("Send")
	d.want("Enter text to send")
	if e.app.busy[sendKey(h.Address)] {
		t.Fatal("an empty body started a send")
	}

	d.click("Cancel")
	if e.dialogOpen("Send text to lab") {
		t.Fatal("Cancel left the Send text window open")
	}
}

func TestUISettingsBasics(t *testing.T) {
	e := newUI(t, false, nil)
	e.openSettings()
	id := hex.EncodeToString(e.app.identity.Public())
	client := e.app.identity.NodeKey(state.RoleClient).Public().String()
	e.want("Database", "Open containing folder",
		"Machine key: "+id, "Client public key: "+client, "Certificates", "Add")
	e.wantNot("Pending approval") // no server running, nothing to list

	e.click("Copy machine key")
	if e.tt.Clipboard() != id {
		t.Fatalf("clipboard = %q, want the machine key", e.tt.Clipboard())
	}
	e.click("Copy client public key")
	if e.tt.Clipboard() != client {
		t.Fatalf("clipboard = %q, want the client key", e.tt.Clipboard())
	}

	// The server's address and its start or stop state are on the self page.
	e.click(selfTitle())
	e.app.serverAddr = "tcExampleServerAddress"
	e.tt.Frame()
	e.want("Server address: tcExampleServerAddress")
	e.click("Copy server address")
	if e.tt.Clipboard() != "tcExampleServerAddress" {
		t.Fatalf("clipboard = %q", e.tt.Clipboard())
	}

	// While a start or stop is in progress, say so.
	e.app.serverAddr, e.app.busy = "", map[string]bool{"server": true}
	e.tt.Frame()
	e.want("Server operation in progress…")
	e.wantNot("Server address:")
}

func TestUICertificates(t *testing.T) {
	e := newUI(t, false, nil)
	e.openSettings()

	e.typeInto("Certificate", "not hex!")
	e.click("Add")
	if e.status() != "Certificate is not valid hex" {
		t.Fatalf("status = %q", e.status())
	}
	if certs, _ := e.app.store.Certs(); len(certs) != 0 {
		t.Fatal("a malformed certificate was stored")
	}

	e.app.certDraft = ""
	e.tt.Frame()
	e.click("Certificate")
	e.tt.Type("deadbeef")
	e.click("Add")
	e.idle()
	if e.status() != "Certificate saved" {
		t.Fatalf("status = %q", e.status())
	}
	certs, _ := e.app.store.Certs()
	if len(certs) != 1 || hex.EncodeToString(certs[0]) != "deadbeef" {
		t.Fatalf("stored certificates = %x", certs)
	}
	if e.app.certDraft != "" {
		t.Fatalf("the draft was kept after saving: %q", e.app.certDraft)
	}
	// The dialer presents what is stored.
	if got := e.app.dialing.Certs(); len(got) != 1 || hex.EncodeToString(got[0]) != "deadbeef" {
		t.Fatalf("dialing certificates = %x", got)
	}
}

func TestUIClientListsAreHiddenWithoutAServer(t *testing.T) {
	e := newUI(t, false, nil)
	e.wantNot("Pending approval", "Approved", "Blocked")
}

// An approved node lists under Approved, attributed to this machine; the admin
// can block it, and approve it again. The lists are on the self page.
func TestUIApproveAndBlock(t *testing.T) {
	e := newUI(t, true, nil)
	k := key.NewNode().Public()
	if err := e.gate.Approve(k); err != nil {
		t.Fatal(err)
	}
	e.refresh()
	if r, _ := clientRow(e.gate, k); r.State != admission.Approved || r.CA != e.gate.SelfFingerprint() {
		t.Fatalf("row = %+v", r)
	}
	e.want(k.String(), "this machine, until")
	e.wantNot("no certificate")
	if got := len(e.app.clients[admission.Approved]); got != 1 {
		t.Fatalf("approved list has %d rows", got)
	}

	// Block takes it away; it moves to Blocked, where Approve lifts the block.
	e.click("Block")
	e.idle()
	if r, _ := clientRow(e.gate, k); r.State != admission.Blocked {
		t.Fatalf("row = %+v", r)
	}
	if !strings.HasPrefix(e.status(), "Blocked ") {
		t.Fatalf("status = %q", e.status())
	}
	e.refresh()
	e.wantNot("this machine, until")
	e.want(k.String(), "Approve")

	e.click("Approve")
	e.idle()
	if r, _ := clientRow(e.gate, k); r.State != admission.Approved {
		t.Fatalf("row after un-blocking = %+v", r)
	}
	e.refresh()
	e.want("this machine, until")
}

// Several rows in one list: each button acts on its own row.
func TestUIEachButtonActsOnItsOwnRow(t *testing.T) {
	e := newUI(t, true, nil)
	a, b := key.NewNode().Public(), key.NewNode().Public()
	for _, k := range []key.NodePublic{a, b} {
		if err := e.gate.Approve(k); err != nil {
			t.Fatal(err)
		}
	}
	e.refresh()
	e.want(a.String(), b.String())

	e.click("Block " + a.String())
	e.idle()
	if r, _ := clientRow(e.gate, a); r.State != admission.Blocked {
		t.Fatalf("a=%+v: Block on a's row acted on the wrong node", r)
	}
	if r, _ := clientRow(e.gate, b); r.State != admission.Approved {
		t.Fatalf("b=%+v: Block on a's row changed b", r)
	}
	e.click("Approve " + a.String())
	e.idle()
	if r, _ := clientRow(e.gate, a); r.State != admission.Approved {
		t.Fatalf("a=%+v after Approve on a's row", r)
	}
}

func TestUIEmptyListsSayNone(t *testing.T) {
	e := newUI(t, true, nil)
	e.refresh()
	got := 0
	for _, s := range e.tt.Texts() {
		if s == "None" {
			got++
		}
	}
	if got != 3 {
		t.Fatalf("%d lists say None, want 3 (pending, approved, blocked); visible: %q", got, e.tt.Texts())
	}
}

// The lists are read from the database once a while, not every frame: a change
// made elsewhere (a connection arriving) shows after a refresh.
func TestUIListsFollowTheGateAfterARefresh(t *testing.T) {
	e := newUI(t, true, nil)
	e.wantNot("no certificate")

	k := key.NewNode().Public()
	if err := e.gate.Approve(k); err != nil {
		t.Fatal(err)
	}
	e.refresh()
	e.want(k.String(), "this machine, until")

	// Stopping the server clears the lists.
	e.app.gate = nil
	e.refresh()
	e.wantNot("Pending approval", k.String())
}

func TestUIBlockedRowsOfferOnlyApprove(t *testing.T) {
	e := newUI(t, true, nil)
	k := key.NewNode().Public()
	e.gate.Block(k)
	e.refresh()
	e.want("Blocked", k.String(), "Approve")
	e.wantNot("Block ", "None\nNone\nNone")
	// Only the one Approve (the blocked row's), and no Block button.
	approves, blocks := 0, 0
	for _, s := range e.tt.Texts() {
		switch s {
		case "Approve":
			approves++
		case "Block":
			blocks++
		}
	}
	if approves != 1 || blocks != 0 {
		t.Fatalf("approve buttons=%d block buttons=%d; want 1 and 0", approves, blocks)
	}
}

// A text received on the port is saved, shown in a notification, and listed in
// the Inbox, where it can be copied and deleted. Clicking its notification
// opens the self page, which holds the Inbox.
func TestUIReceivedTextInbox(t *testing.T) {
	e := newUI(t, false, nil)
	// The receive path queues its UI work here, and the test runs it on its own
	// goroutine, as the view does.
	queue := make(chan func(), 16)
	e.app.runOnUI = func(fn func()) { queue <- fn }
	notes := make(chan mygo.NotificationOptions, 4)
	e.app.notify = func(o mygo.NotificationOptions) error {
		notes <- o
		return nil
	}
	pump := func(until func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !until() {
			select {
			case fn := <-queue:
				fn()
			case <-deadline:
				t.Fatal("timed out waiting for the app")
			}
		}
	}

	const body = "hello from alice"
	from := &net.TCPAddr{IP: net.IPv4(100, 64, 0, 9), Port: 4000}
	e.app.receiveText(textsender.Text{From: from, Body: body})
	pump(func() bool { return len(e.app.inbox) == 1 })

	note := <-notes
	if want := fmt.Sprintf("mochi-text:%d", e.app.inbox[0].ID); note.ID != want {
		t.Fatalf("notification ID %q, want %q", note.ID, want)
	}
	if note.Title != "Text from 100.64.0.9:4000" || note.Body != body {
		t.Fatalf("notification %q / %q, want the sender and the text", note.Title, note.Body)
	}

	// The click lands on the self page, whatever page was open.
	e.app.goTo(page{kind: pageSettings})
	e.app.notificationClicked(note.ID)
	select {
	case fn := <-queue:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("the Inbox was not reloaded after the click")
	}
	if e.app.page != (page{}) {
		t.Fatalf("page = %+v after the click, want the self page", e.app.page)
	}
	e.tt.Frame()
	e.want("Inbox", "100.64.0.9:4000", body)

	e.click("Copy text")
	if got := e.tt.Clipboard(); got != body {
		t.Fatalf("clipboard = %q, want %q", got, body)
	}

	e.click("Delete text")
	pump(func() bool { return len(e.app.inbox) == 0 })
	e.tt.Frame()
	e.wantNot(body)
}

// While Connect is pending the tile says so, and the rest of the window keeps
// answering. A second click on the busy tile starts no second Connect, and
// releasing the dial connects.
func TestSlowConnectDoesNotBlockTheUI(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	slow := &toggleConnector{name: "Plain", hold: make(chan struct{})}
	e := newUI(t, false, []state.Host{h}, slow)
	e.click("lab")
	e.click("Plain launcher")

	e.want("Plain…")
	e.click("Settings")
	e.want("Database")
	e.click("lab")
	e.want("Plain…")
	e.click("Plain launcher")

	close(slow.hold)
	e.idle()
	if slow.connects != 1 {
		t.Fatalf("connects = %d, want the one pending Connect", slow.connects)
	}
	if len(e.app.connections[h.Address]) != 1 || e.status() != "Plain connected" {
		t.Fatalf("connections=%v status=%q", e.app.connections, e.status())
	}
}

// Removing a host while its Connect is pending disconnects the client that the
// Connect hands back late, and keeps it out of the connections.
func TestRemovingAHostDisconnectsALateConnect(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	other := state.Host{Name: "keep", Address: testAddress(t)}
	slow := &toggleConnector{name: "Plain", hold: make(chan struct{}), gone: make(chan struct{})}
	e := newUI(t, false, []state.Host{h, other}, slow)
	e.click("lab")
	e.click("Plain launcher")

	e.click("Remove host")
	e.dialog("Remove lab?").click("Yes")
	close(slow.hold)
	e.idle()

	select {
	case <-slow.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the late client was not disconnected")
	}
	if len(slow.clients) != 1 || slow.clients[0].disconnects != 1 {
		t.Fatalf("late clients = %d, want one, disconnected once", len(slow.clients))
	}
	if len(e.app.connections[h.Address]) != 0 || len(e.app.hosts) != 1 {
		t.Fatalf("connections=%v hosts=%+v", e.app.connections, e.app.hosts)
	}
}
