package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"image/color"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

const (
	uiWidth  = 1100
	uiHeight = 700
)

// toggleConnector is a connector whose enabled state the test controls.
type toggleConnector struct {
	name      string
	on        map[string]bool
	enableErr error
	enables   int
	disables  int
	url       string
	serverSwitch
}

func (c *toggleConnector) ServerServe(*ServerServices) {}

func (c *toggleConnector) Name() string        { return c.name }
func (c *toggleConnector) Icon() *ui.SVG       { return webdavIcon }
func (c *toggleConnector) SetHosts([]Host)     {}
func (c *toggleConnector) CLICommand() string  { return "" }
func (c *toggleConnector) Enabled(h Host) bool { return c.on[h.Address] }
func (c *toggleConnector) RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error {
	return nil
}
func (c *toggleConnector) Enable(h Host) error {
	c.enables++
	if c.enableErr != nil {
		return c.enableErr
	}
	c.on[h.Address] = true
	return nil
}
func (c *toggleConnector) Disable(h Host) error {
	c.disables++
	delete(c.on, h.Address)
	return nil
}

// urlConnector also reports an address to copy, as WebDAV and RustDesk do.
type urlConnector struct{ toggleConnector }

func (c *urlConnector) URL(Host) string { return c.url }

// uiEnv is the app under test with its state in a temporary database, and the
// tester that drives its view.
type uiEnv struct {
	t     *testing.T
	app   *app
	tt    *ui.Tester
	gate  *Gate
	theme ui.Theme
	ext   ed25519.PrivateKey // a key the gate trusts besides its own
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
func newUI(t *testing.T, withGate bool, hosts []Host, connectors ...Connector) *uiEnv {
	t.Helper()
	if err := registerFonts(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
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
	a := &app{
		hosts:    hosts,
		store:    store,
		identity: id,
		dialing: Dialing{Key: id.NodeKey(RoleClient), Certs: func() [][]byte {
			certs, _ := store.Certs()
			return certs
		}},
		connectors: connectors,
	}
	e := &uiEnv{t: t, app: a}
	if withGate {
		extPub, extPriv, _ := ed25519.GenerateKey(nil)
		e.ext = extPriv
		g, err := NewGate(store.db, id.Private(), []ed25519.PublicKey{extPub})
		if err != nil {
			t.Fatal(err)
		}
		g.srv = &tailcat.Server{} // never started: DisconnectClient is a no-op
		a.gate, e.gate = g, g
	}
	e.tt = ui.NewTester(func(c *ui.Context) {
		e.theme = *c.Theme()
		a.view(c)
	}, uiWidth, uiHeight)
	e.tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 70, Right: 140})
	return e
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

// refresh does what the app's 2 s ticker does while Settings is open.
func (e *uiEnv) refresh() {
	e.app.refreshClients()
	e.tt.Frame()
}

func (e *uiEnv) status() string { return e.app.status }

func (e *uiEnv) openSettings() {
	e.t.Helper()
	e.click("⚙  Settings")
	e.want("Settings")
}

func (e *uiEnv) pixel(x, y float32) color.RGBA { return e.tt.Image().RGBAAt(int(x), int(y)) }

func sameColor(c color.RGBA, w ui.Color) bool { return c.R == w.R && c.G == w.G && c.B == w.B }

func TestUIEmptyState(t *testing.T) {
	e := newUI(t, false, nil)
	e.want("Mochi", "Select a host from the sidebar or add a new one.", "Hosts", "+  Add host", "⚙  Settings")
	e.wantNot("Launchers", "Remove host")
}

// The layout the sidebar, title bar and status bar were built to have: the
// title bar spans the full width above everything, the sidebar runs from it to
// the bottom, and the status bar sits under the content only.
func TestUILayout(t *testing.T) {
	h := Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []Host{h})
	e.app.status = "Everything is fine"
	e.tt.Frame()

	title, _ := e.tt.Find("Mochi")
	if title.Y > 44 {
		t.Errorf("title is at y=%v, below the 44px title bar", title.Y)
	}
	if title.X < 70 {
		t.Errorf("title starts at x=%v, under the native window controls (70 reserved)", title.X)
	}
	side, _ := e.tt.Find("⚙  Settings")
	if side.X >= 240 {
		t.Errorf("Settings button at x=%v, outside the 240px sidebar", side.X)
	}
	if side.Y < uiHeight-80 {
		t.Errorf("Settings button at y=%v: the sidebar does not reach the bottom (%d)", side.Y, uiHeight)
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

	// A good one is saved, selected and shown.
	e.click("+  Add host")
	e.typeInto("Host name", "lab")
	e.typeInto("Tailcat address", addr)
	e.click("Save host")
	if e.status() != "Host added" {
		t.Fatalf("status = %q", e.status())
	}
	hosts, _ := e.app.store.Hosts()
	if len(hosts) != 1 || hosts[0] != (Host{Name: "lab", Address: addr}) {
		t.Fatalf("saved hosts = %+v", hosts)
	}
	e.want("Launchers", "Host added")
	if e.shows("Add host") {
		t.Fatal("still on the form after saving")
	}
}

func TestUISelectHostAndShowDetails(t *testing.T) {
	h1 := Host{Name: "alpha", Address: testAddress(t)}
	h2 := Host{Name: "beta", Address: testAddress(t)}
	e := newUI(t, false, []Host{h1, h2})
	e.want("alpha", "beta", "Select a host from the sidebar")

	e.click("beta")
	if e.app.selectedHost != h2.Address {
		t.Fatalf("selected %q, want beta's address", e.app.selectedHost)
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
	h := Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []Host{h})
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
	h := Host{Name: "lab", Address: testAddress(t)}
	plain := &toggleConnector{name: "Plain", on: map[string]bool{}}
	withURL := &urlConnector{toggleConnector{name: "Share", on: map[string]bool{}, url: `\\127.0.0.1@4000\DavWWWRoot\tok`}}
	e := newUI(t, false, []Host{h}, plain, withURL)
	e.click("lab")
	e.want("Plain", "Share", "Send text")

	tile, ok := e.tt.Find("Plain launcher")
	if !ok {
		t.Fatalf("no Plain tile; visible: %q", e.tt.Texts())
	}
	probe := func() color.RGBA { return e.pixel(tile.X+6, tile.Y+tile.H/2) }
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a disabled launcher is drawn in the accent color")
	}

	// Enabling: the connector is called, the tile turns accent-filled.
	e.click("Plain launcher")
	if plain.enables != 1 || !plain.on[h.Address] {
		t.Fatalf("enables=%d on=%v", plain.enables, plain.on)
	}
	if e.status() != "Plain enabled" {
		t.Fatalf("status = %q", e.status())
	}
	if !sameColor(probe(), e.theme.Accent) {
		t.Fatalf("an enabled launcher is %v, want the accent color %v", probe(), e.theme.Accent)
	}
	// Its text is inverted: pixels of the label differ from the disabled ones.
	// (Checked via the tile's own color, which is what the label is drawn on.)

	// Disabling returns it to the plain look.
	e.click("Plain launcher")
	if plain.disables != 1 || plain.on[h.Address] || e.status() != "Plain disabled" {
		t.Fatalf("disables=%d on=%v status=%q", plain.disables, plain.on, e.status())
	}
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a disabled launcher stayed accent-filled")
	}

	// A failure is shown, and the tile stays plain.
	plain.enableErr = errors.New("no route to host")
	e.click("Plain launcher")
	if e.status() != "Plain connector failed: no route to host" {
		t.Fatalf("status = %q", e.status())
	}
	if sameColor(probe(), e.theme.Accent) {
		t.Fatal("a failed enable left the tile accent-filled")
	}

	// A connector with an address copies it, and says so.
	e.click("Share launcher")
	if got := e.tt.Clipboard(); got != withURL.url {
		t.Fatalf("clipboard = %q, want the share address", got)
	}
	if !strings.HasPrefix(e.status(), "Share address copied: ") {
		t.Fatalf("status = %q", e.status())
	}
}

func TestUIRemoveHost(t *testing.T) {
	h := Host{Name: "lab", Address: testAddress(t)}
	other := Host{Name: "keep", Address: testAddress(t)}
	c := &toggleConnector{name: "Plain", on: map[string]bool{}}
	e := newUI(t, false, []Host{h, other}, c)
	e.click("lab")

	e.click("Remove host")
	e.want("Remove lab?", "This removes the host and its SSH config entry.", "Yes", "No")

	// No keeps everything.
	e.click("No")
	e.wantNot("Remove lab?")
	if len(e.app.hosts) != 2 || c.disables != 0 {
		t.Fatalf("hosts=%d disables=%d after No", len(e.app.hosts), c.disables)
	}

	// Yes disables the connectors, deletes the host from memory and the store,
	// and falls back to the empty page.
	e.click("Remove host")
	e.click("Yes")
	if c.disables != 1 {
		t.Fatalf("connector disabled %d times, want once", c.disables)
	}
	if len(e.app.hosts) != 1 || e.app.hosts[0] != other {
		t.Fatalf("hosts = %+v", e.app.hosts)
	}
	stored, _ := e.app.store.Hosts()
	if len(stored) != 1 || stored[0] != other {
		t.Fatalf("stored hosts = %+v", stored)
	}
	e.want("keep", "Host removed")
	e.wantNot("Remove lab?", h.Address, nodeIDFromAddress(h.Address))
}

func TestUISendTextDialog(t *testing.T) {
	h := Host{Name: "lab", Address: testAddress(t)}
	e := newUI(t, false, []Host{h})
	e.click("lab")

	e.click("Send text launcher")
	e.want("Send text to lab", "Send", "Cancel")

	// Validation happens before anything is sent.
	e.typeInto("Port", "70000")
	e.typeInto("Text to send", "hello")
	e.click("Send")
	if e.status() == "" || strings.HasPrefix(e.status(), "Sending") {
		t.Fatalf("a bad port was sent: status = %q", e.status())
	}
	badPortStatus := e.status()

	e.app.sendPort = ""
	e.app.sendBody = ""
	e.tt.Frame()
	e.click("Send")
	if e.status() != "Enter text to send" {
		t.Fatalf("status = %q (the bad-port message was %q)", e.status(), badPortStatus)
	}
	if e.app.sendBusy {
		t.Fatal("nothing was sent, but the dialog is busy")
	}

	e.click("Cancel")
	e.wantNot("Send text to lab")
}

func TestUISettingsBasics(t *testing.T) {
	e := newUI(t, false, nil)
	e.openSettings()
	id := hex.EncodeToString(e.app.identity.Public())
	client := e.app.identity.NodeKey(RoleClient).Public().String()
	e.want("Enable Tailcat server", "Database", "Open containing folder",
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

	// A running server shows its address, and offers to copy it.
	e.app.serverAddr = "tcExampleServerAddress"
	e.tt.Frame()
	e.want("Server address: tcExampleServerAddress")
	e.click("Copy server address")
	if e.tt.Clipboard() != "tcExampleServerAddress" {
		t.Fatalf("clipboard = %q", e.tt.Clipboard())
	}

	// While a start or stop is in progress, say so.
	e.app.serverAddr, e.app.serverBusy = "", true
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
	e.openSettings()
	e.wantNot("Pending approval", "Approved", "Blocked")
}

// A node the host has not seen approved waits under Pending approval; the
// admin can approve it, block it, and approve it again.
func TestUIApproveAndBlock(t *testing.T) {
	e := newUI(t, true, nil)
	k := key.NewNode().Public()
	if !e.gate.allowClient(k) { // a stranger connects and gets a probation slot
		t.Fatal("stranger refused")
	}
	e.openSettings()
	e.want("Pending approval", "Approved", "Blocked", k.String(), "no certificate", "Approve", "Block")

	// Approve moves it to Approved, attributed to this machine.
	e.click("Approve")
	if r := e.gate.get(k); r.State != Approved || r.CA != e.gate.selfFP {
		t.Fatalf("row = %+v", r)
	}
	if !strings.HasPrefix(e.status(), "Approved ") {
		t.Fatalf("status = %q", e.status())
	}
	e.want(k.String(), "this machine, until")
	e.wantNot("no certificate")
	if got := len(e.app.clients[Approved]); got != 1 {
		t.Fatalf("approved list has %d rows", got)
	}

	// Block takes it away; it moves to Blocked, where Approve lifts the block.
	e.click("Block")
	if r := e.gate.get(k); r.State != Blocked {
		t.Fatalf("row = %+v", r)
	}
	if !strings.HasPrefix(e.status(), "Blocked ") {
		t.Fatalf("status = %q", e.status())
	}
	e.wantNot("this machine, until")
	e.want(k.String(), "Approve")
	if e.gate.allowClient(k) {
		t.Fatal("a blocked node was admitted")
	}

	e.click("Approve")
	if r := e.gate.get(k); r.State != Approved {
		t.Fatalf("row after un-blocking = %+v", r)
	}
	e.want("this machine, until")
}

// Several rows in one list: each button acts on its own row.
func TestUIEachButtonActsOnItsOwnRow(t *testing.T) {
	e := newUI(t, true, nil)
	a, b := key.NewNode().Public(), key.NewNode().Public()
	e.gate.allowClient(a)
	e.gate.allowClient(b)
	e.openSettings()
	e.refresh()
	e.want(a.String(), b.String())

	e.click("Approve " + b.String())
	if e.gate.get(b).State != Approved || e.gate.get(a).State != Probation {
		t.Fatalf("a=%+v b=%+v: Approve on b's row acted on the wrong node", e.gate.get(a), e.gate.get(b))
	}
	e.click("Block " + a.String())
	if e.gate.get(a).State != Blocked || e.gate.get(b).State != Approved {
		t.Fatalf("a=%+v b=%+v: Block on a's row acted on the wrong node", e.gate.get(a), e.gate.get(b))
	}
}

// What the lists say about each node: who approved it, why a rejected one was
// turned away, and which nodes are known.
func TestUIListDetails(t *testing.T) {
	e := newUI(t, true, nil)
	g := e.gate

	// Approved by a trusted external key: shows the certificate's name.
	byExt := key.NewNode().Public()
	extPub := e.ext.Public().(ed25519.PublicKey)
	cert, err := Issue(e.ext, byExt, "alice@example.com", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.admit(byExt, cert); err != nil {
		t.Fatal(err)
	}
	// Approved by this machine.
	byHost := key.NewNode().Public()
	if err := g.Approve(byHost); err != nil {
		t.Fatal(err)
	}
	// Rejected with a reason, and a known node whose certificate expired.
	stranger := key.NewNode().Public()
	g.allowClient(stranger)
	g.reject(stranger, "no certificate")
	known := key.NewNode().Public()
	g.put(Client{Key: known, State: Approved, CA: Fingerprint(extPub), Name: "bob@example.com", Expires: time.Now().Add(time.Hour)})
	g.reject(known, "certificate expired")

	e.openSettings()
	e.refresh()
	e.want(byExt.String(), "alice@example.com, until", byHost.String(), "this machine, until",
		stranger.String(), "no certificate", known.String(), "bob@example.com: certificate expired")
}

func TestUIEmptyListsSayNone(t *testing.T) {
	e := newUI(t, true, nil)
	e.openSettings()
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
	e.openSettings()
	e.wantNot("no certificate")

	k := key.NewNode().Public()
	e.gate.allowClient(k)
	e.refresh()
	e.want(k.String(), "no certificate")

	// Stopping the server clears the lists.
	e.app.gate = nil
	e.refresh()
	e.wantNot("Pending approval", k.String())
}

func TestUIBlockedRowsOfferOnlyApprove(t *testing.T) {
	e := newUI(t, true, nil)
	k := key.NewNode().Public()
	e.gate.Block(k)
	e.openSettings()
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
