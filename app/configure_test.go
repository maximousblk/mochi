package app

import (
	"testing"

	"github.com/maximousblk/mochi/internal/connector"
	"github.com/maximousblk/mochi/internal/state"
)

// configure right-clicks the launcher tile labeled tile and chooses Configure…
// from its menu. The dialog's config loads off the UI thread, so the window
// appears once the app is idle.
func (e *uiEnv) configure(tile string) {
	e.t.Helper()
	revealInView(e.t, e.tt, tile)
	if err := e.tt.RightClick(tile); err != nil {
		e.t.Fatalf("%v\nvisible: %q", err, e.tt.Texts())
	}
	if err := e.tt.ChooseMenuItem("Configure\u2026"); err != nil {
		e.t.Fatalf("%v; menu: %q", err, e.tt.Menu())
	}
	e.idle()
}

// Changing a serving launcher's port in its Configure window restarts it on the
// new port, stops the old one, and saves the new setting. The Send text receiver
// is the launcher here, since it serves on the in-process ports registry.
func TestUIConfigureServerPortRestartsServer(t *testing.T) {
	e := newUI(t, false, nil)
	recv := e.app.servers[len(e.app.servers)-1]
	if err := recv.Listen(connector.Config{"port": "1"}); err != nil {
		t.Fatal(err)
	}
	e.app.serving[recv.Name()] = true

	e.configure("Send text launcher")
	d := e.dialog("Send text server")
	d.want("Tailcat port")
	d.replace("Tailcat port", "2005")
	d.click("Save")
	e.idle()

	if e.app.ports.Registered(1) || !e.app.ports.Registered(2005) {
		t.Fatalf("after Save: port 1 served=%v, port 2005 served=%v; want only 2005",
			e.app.ports.Registered(1), e.app.ports.Registered(2005))
	}
	saved, err := e.app.store.LauncherConfig(recv.Name(), "")
	if err != nil || saved["port"] != "2005" {
		t.Fatalf("saved server config = %v, %v; want port 2005", saved, err)
	}
	if !e.app.serving[recv.Name()] {
		t.Fatal("the server stopped after its port changed")
	}
	if e.dialogOpen("Send text server") {
		t.Fatal("the dialog is still open after Save")
	}
}

// An invalid port keeps the window open with the error and changes nothing.
func TestUIConfigureInvalidPortChangesNothing(t *testing.T) {
	e := newUI(t, false, nil)
	recv := e.app.servers[len(e.app.servers)-1]
	if err := recv.Listen(connector.Config{"port": "1"}); err != nil {
		t.Fatal(err)
	}
	e.app.serving[recv.Name()] = true

	e.configure("Send text launcher")
	d := e.dialog("Send text server")
	d.replace("Tailcat port", "70000")
	d.click("Save")
	e.idle()

	d.want("invalid TCP port")
	if !e.dialogOpen("Send text server") {
		t.Fatal("an invalid port closed the window")
	}
	if !e.app.ports.Registered(1) {
		t.Fatal("an invalid port changed what the server serves")
	}
	if saved, err := e.app.store.LauncherConfig(recv.Name(), ""); err != nil || len(saved) != 0 {
		t.Fatalf("an invalid port was saved: %v, %v", saved, err)
	}
}

// Right-clicking a host's launcher tile edits that host's client config, and
// removing the host deletes it.
func TestUIConfigureHostTileSavesAndRemoveDeletes(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	conn := &toggleConnector{name: "Files"}
	e := newUI(t, false, []state.Host{h}, conn)
	e.click("lab")

	e.configure("Files launcher")
	d := e.dialog("Files for lab")
	d.want("Host's port")
	d.replace("Host's port", "9100")
	d.click("Save")
	e.idle()

	if saved, err := e.app.store.LauncherConfig("Files", h.Address); err != nil || saved["port"] != "9100" {
		t.Fatalf("saved client config = %v, %v; want port 9100", saved, err)
	}
	if e.dialogOpen("Files for lab") {
		t.Fatal("the dialog is still open after Save")
	}

	e.click("Remove host")
	e.dialog("Remove lab?").click("Yes")
	e.idle()
	if saved, err := e.app.store.LauncherConfig("Files", h.Address); err != nil || len(saved) != 0 {
		t.Fatalf("client config of a removed host = %v, %v; want none", saved, err)
	}
}

// Opening the same Configure dialog again focuses the window already open, and
// a different launcher or host gets a window of its own. The main window keeps
// answering while they are open, since the dialogs are not modal.
func TestConfigureDialogsAreIndependentWindows(t *testing.T) {
	h := state.Host{Name: "lab", Address: testAddress(t)}
	conn := &toggleConnector{name: "Files"}
	e := newUI(t, false, []state.Host{h}, conn)

	e.configure("Send text launcher")
	e.configure("Send text launcher")
	if n := e.windows.openCount(); n != 1 {
		t.Fatalf("%d windows open after opening the same dialog twice, want 1", n)
	}

	e.click("lab")
	e.configure("Files launcher")
	if n := e.windows.openCount(); n != 2 {
		t.Fatalf("%d windows open with two different dialogs, want 2", n)
	}
	if !e.dialogOpen("Send text server") || !e.dialogOpen("Files for lab") {
		t.Fatal("both dialogs should be open")
	}

	// The main window still works while the dialogs are open.
	e.click("Settings")
	e.want("Database")
	e.wantNot("Launchers")
}
