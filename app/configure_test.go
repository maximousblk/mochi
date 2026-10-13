package app

import (
	"testing"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
)

// configure right-clicks the launcher tile labeled tile and chooses Configure…
// from its menu.
func (e *uiEnv) configure(tile string) {
	e.t.Helper()
	revealInView(e.t, e.tt, tile)
	if err := e.tt.RightClick(tile); err != nil {
		e.t.Fatalf("%v\nvisible: %q", err, e.tt.Texts())
	}
	if err := e.tt.ChooseMenuItem("Configure\u2026"); err != nil {
		e.t.Fatalf("%v; menu: %q", err, e.tt.Menu())
	}
}

// Changing a serving launcher's port in Settings restarts it on the new port,
// stops the old one, and saves the new setting. The Send text receiver is the
// launcher here, since it serves on the in-process ports registry.
func TestUIConfigureServerPortRestartsServer(t *testing.T) {
	e := newUI(t, false, nil)
	recv := e.app.servers[len(e.app.servers)-1]
	if err := recv.Listen(connector.Config{"port": "1"}); err != nil {
		t.Fatal(err)
	}
	e.app.serving[recv.Name()] = true

	e.openSettings()
	e.configure("Send text launcher")
	e.want("Send text server", "Tailcat port")
	e.app.config.values[0] = "2005"
	e.click("Save")

	if e.app.ports.Registered(1) || !e.app.ports.Registered(2005) {
		t.Fatalf("after Save: port 1 served=%v, port 2005 served=%v; want only 2005",
			e.app.ports.Registered(1), e.app.ports.Registered(2005))
	}
	saved, err := e.app.store.Config(connector.ServerConfigKey(recv))
	if err != nil || saved["port"] != "2005" {
		t.Fatalf("saved server config = %v, %v; want port 2005", saved, err)
	}
	if !e.app.serving[recv.Name()] {
		t.Fatal("the server stopped after its port changed")
	}
	if e.shows("Send text server") {
		t.Fatal("the dialog is still open after Save")
	}
}

// An invalid port keeps the dialog open with the error and changes nothing.
func TestUIConfigureInvalidPortChangesNothing(t *testing.T) {
	e := newUI(t, false, nil)
	recv := e.app.servers[len(e.app.servers)-1]
	if err := recv.Listen(connector.Config{"port": "1"}); err != nil {
		t.Fatal(err)
	}
	e.app.serving[recv.Name()] = true

	e.openSettings()
	e.configure("Send text launcher")
	e.app.config.values[0] = "70000"
	e.click("Save")

	e.want("Send text server", "invalid TCP port")
	if !e.app.ports.Registered(1) {
		t.Fatal("an invalid port changed what the server serves")
	}
	if saved, err := e.app.store.Config(connector.ServerConfigKey(recv)); err != nil || len(saved) != 0 {
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
	e.want("Files for lab", "Host's port")
	e.app.config.values[0] = "9100"
	e.click("Save")

	key := connector.ClientConfigKey("Files", h.Address)
	if saved, err := e.app.store.Config(key); err != nil || saved["port"] != "9100" {
		t.Fatalf("saved client config = %v, %v; want port 9100", saved, err)
	}
	e.wantNot("Files for lab")

	e.click("Remove host")
	e.click("Yes")
	if saved, err := e.app.store.Config(key); err != nil || len(saved) != 0 {
		t.Fatalf("client config of a removed host = %v, %v; want none", saved, err)
	}
}
