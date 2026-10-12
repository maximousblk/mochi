// Package app is Mochi's window: the host list, the launchers, Settings, and the
// tray icon that keeps the app running while the window is closed.
package app

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/connector/rustdesk"
	"github.com/ashwin-y/mochi/internal/connector/ssh"
	"github.com/ashwin-y/mochi/internal/connector/webdav"
	"github.com/ashwin-y/mochi/internal/server"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

type app struct {
	hosts         []state.Host
	selectedHost  string
	addingHost    bool
	showSettings  bool
	nameDraft     string
	addrDraft     string
	status        string
	confirmRemove bool
	sendOpen      bool
	sendBody      string
	sendPort      string
	sendBusy      bool
	serverAddr    string
	serverEnabled bool
	serverBusy    bool
	server        *tailcat.Server
	gate          *admission.Gate
	stopGate      context.CancelFunc
	identity      state.Identity
	dialing       admission.Dialing
	trusted       []ed25519.PublicKey
	certDraft     string
	clients       map[string][]admission.Client
	store         *state.StateStore
	lastTrayClick time.Time // when the tray icon was last clicked, for double clicks
	connectors    []connector.Connector
	ports         *connector.Ports
	window        *mygo.Window
	tray          *mygo.Tray
	quitting      bool

	// serving is each launcher's server-side switch, by name, as the app last
	// set it. connections holds the clients open now, by host address and then
	// by launcher name. Clients are not saved: they exist only while open.
	serving     map[string]bool
	connections map[string]map[string]connector.Client

	// pageScroll is where the content scrolls, so the app can bring it back to
	// the top, as the Settings page needs after a switch is flipped.
	pageScroll ui.ScrollState

	// unhealthy names the launchers that are not healthy, as of the last check.
	// Healthy may dial, so the view reads this instead of calling it on every
	// frame. The view draws a launcher yellow only while its switch is on.
	unhealthy map[string]bool

	// Seams for tests; all nil in the real app.
	runOnUI    func(func())
	relay      *tailcfg.DERPRegion              // serve through this relay instead of picking a public one
	openPath   func(string) error               // opens a folder; mygo.Shell.OpenPath when nil
	serveExtra func(port uint16) func(net.Conn) // more ports for the server to serve
	copyText   func(string)                     // copies text; mygo's clipboard when nil
}

// Run starts the app and blocks until it exits. args are the command-line
// arguments after the program name.
func Run(args []string) error {
	if err := registerFonts(); err != nil {
		return fmt.Errorf("registering Ioskeley Mono: %w", err)
	}
	// Mochi lives in the menu bar until its window opens: no Dock icon in
	// the background. showWindow and the close handler switch it per window.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	statePath, err := state.Path()
	if err != nil {
		return err
	}
	legacyPath, err := state.LegacyPath()
	if err != nil {
		return err
	}
	store, err := state.OpenState(statePath, legacyPath)
	if err != nil {
		return fmt.Errorf("opening state: %w", err)
	}
	// A GUI app has no console, so what it logs also goes to mochi.log beside
	// the database. If the file cannot be opened, logging stays on stderr.
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(statePath), "mochi.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	a := &app{}
	if err := a.load(store); err != nil {
		return err
	}

	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{
			Title:          "Mochi",
			Width:          720,
			Height:         560,
			StateKey:       "main",
			Hidden:         true,
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: 44,
			Content:        ui.View(a.view),
		})
		a.window.OnClose(func(e *mygo.CloseEvent) {
			if !a.quitting {
				e.PreventDefault()
				a.window.Hide()
				mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			}
		})
		a.tray, _ = mygo.NewTray(mygo.TrayOptions{
			Icon:    trayIcon,
			ToolTip: "Mochi",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "Open Mochi", Click: func(*mygo.MenuItem, *mygo.Window) { a.showWindow() }},
				mygo.Separator(),
				{Label: "Exit", Click: func(*mygo.MenuItem, *mygo.Window) { a.exit() }},
			}),
		})
		if a.tray == nil {
			log.Println("tray is unavailable; Mochi will remain in the background without a tray icon")
		} else {
			a.tray.OnClick(a.trayClicked)
		}
		if a.serverEnabled {
			a.startServer()
		}
		go a.watchHealth()
		go a.watchIdle()
	})
	mygo.App.OnWindowAllClosed(func() {})
	return mygo.App.Run()
}

// load reads everything the app starts from out of store: hosts, the server
// setting, this machine's identity, the trusted keys and the launchers.
func (a *app) load(store *state.StateStore) (err error) {
	a.store = store
	if a.hosts, err = store.Hosts(); err != nil {
		return fmt.Errorf("loading hosts: %w", err)
	}
	if a.serverEnabled, err = store.BoolSetting("server_enabled"); err != nil {
		return fmt.Errorf("loading server setting: %w", err)
	}
	if a.identity, err = store.Identity(); err != nil {
		return fmt.Errorf("loading Tailcat client identity: %w", err)
	}
	if a.trusted, err = state.TrustedKeys(); err != nil {
		return fmt.Errorf("loading trusted keys: %w", err)
	}
	a.dialing = admission.Dialing{Key: a.identity.NodeKey(state.RoleClient), Certs: func() [][]byte {
		certs, _ := store.Certs()
		return certs
	}}
	a.ports = connector.NewPorts()
	a.serving = map[string]bool{}
	a.connections = map[string]map[string]connector.Client{}
	a.connectors = newConnectors(a.ports, a.identity.Seed(), a.copyToClipboard, a.dialing)
	for _, c := range a.connectors {
		on, err := store.BoolSetting(connector.SettingKey(c))
		switch {
		case err == nil && on:
			if err := c.Listen(); err != nil {
				a.status = c.Name() + " server not started: " + err.Error()
			} else {
				a.serving[c.Name()] = true
			}
		case err == nil:
			if err := c.Stop(); err != nil {
				a.status = c.Name() + " server not stopped: " + err.Error()
			}
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("loading %s server setting: %w", c.Name(), err)
		}
	}
	if len(a.hosts) > 0 {
		a.selectedHost = a.hosts[0].Address
	}
	return nil
}

// newConnectors returns the launchers in the order the UI shows them.
func newConnectors(ports *connector.Ports, seed []byte, copyText func(string), dialing admission.Dialing) []connector.Connector {
	return []connector.Connector{
		ssh.New(ports, seed, copyText, sshIcon, dialing),
		webdav.New(ports, copyText, webdavIcon, dialing),
		rustdesk.New(ports, rustdeskIcon, dialing),
	}
}

// update runs fn on the UI thread. Tests set runOnUI to run it where they
// can step the view; with neither it is the window's.
func (a *app) update(fn func()) {
	if a.runOnUI != nil {
		a.runOnUI(fn)
		return
	}
	a.window.Update(fn)
}

// copyToClipboard puts text on the clipboard.
func (a *app) copyToClipboard(text string) {
	if a.copyText != nil {
		a.copyText(text)
		return
	}
	mygo.Clipboard.WriteText(text)
}

// healthCheckInterval is how often the launchers' health is checked.
const healthCheckInterval = 5 * time.Second

// unhealthyNow asks each launcher whether it is healthy, and returns the names
// of those that are not. A launcher that is off is not healthy, so the view
// draws one yellow only while its switch is on. Healthy may dial, so this runs
// off the UI thread.
func (a *app) unhealthyNow() map[string]bool {
	bad := map[string]bool{}
	for _, c := range a.connectors {
		if !c.Healthy() {
			bad[c.Name()] = true
		}
	}
	return bad
}

// checkHealth refreshes unhealthy in the background.
func (a *app) checkHealth() {
	go func() {
		bad := a.unhealthyNow()
		a.update(func() { a.unhealthy = bad })
	}()
}

// watchHealth checks the launchers' health now and then, for as long as the
// app runs.
func (a *app) watchHealth() {
	for {
		bad := a.unhealthyNow()
		a.update(func() { a.unhealthy = bad })
		time.Sleep(healthCheckInterval)
	}
}

// toggleServing turns a launcher's server side on or off, and saves its switch.
func (a *app) toggleServing(lc connector.Connector) {
	on := a.serving[lc.Name()]
	var err error
	if on {
		err = lc.Stop()
	} else {
		err = lc.Listen()
	}
	if err != nil {
		a.status = lc.Name() + " server not changed: " + err.Error()
	} else {
		a.serving[lc.Name()] = !on
		if err := a.store.SetBool(connector.SettingKey(lc), !on); err != nil {
			a.status = "Saving " + lc.Name() + " setting failed: " + err.Error()
		}
	}
	a.checkHealth()
}

func (a *app) showWindow() {
	if a.window == nil {
		return
	}
	// The Dock icon shows only while the window is open (macOS; no-op elsewhere).
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	a.window.Show()
	a.window.Focus()
}

// trayDoubleClickInterval is how close two clicks on the tray icon must be to
// count as a double click: the Windows default. MyGo reports single clicks only.
const trayDoubleClickInterval = 500 * time.Millisecond

// trayClicked opens the window on the second of two quick clicks on the tray
// icon. Windows reports each click of a double click, so two clicks close
// together are one double click; a third click starts again.
func (a *app) trayClicked() {
	now := time.Now()
	if now.Sub(a.lastTrayClick) <= trayDoubleClickInterval {
		a.lastTrayClick = time.Time{}
		a.showWindow()
		return
	}
	a.lastTrayClick = now
}

func (a *app) exit() {
	a.quitting = true
	a.disconnectAll()
	if a.stopGate != nil {
		a.stopGate()
	}
	if a.server != nil {
		_ = a.server.Close()
		a.server = nil
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	mygo.App.Quit()
}

func (a *app) addHost() {
	if a.nameDraft == "" || a.addrDraft == "" {
		a.status = "Host name and address are required"
		return
	}
	if _, err := tailcat.ParseAddr(tailcat.Addr(a.addrDraft)); err != nil {
		a.status = fmt.Sprintf("Invalid Tailcat address: %v", err)
		return
	}
	a.hosts = append(a.hosts, state.Host{Name: a.nameDraft, Address: a.addrDraft})
	a.selectedHost = a.addrDraft
	a.addingHost = false
	a.showSettings = false
	a.nameDraft, a.addrDraft = "", ""
	if err := a.store.AddHost(a.hosts[len(a.hosts)-1]); err != nil {
		a.status = fmt.Sprintf("Host added, but save failed: %v", err)
		a.hosts = a.hosts[:len(a.hosts)-1]
		return
	}
	a.status = "Host added"
}

func (a *app) removeHost(index int) error {
	if index < 0 || index >= len(a.hosts) {
		return nil
	}
	host := a.hosts[index]
	if err := a.disconnectHost(host.Address); err != nil {
		return err
	}
	if err := a.store.DeleteHost(host.Address); err != nil {
		return fmt.Errorf("delete host from state: %w", err)
	}
	a.hosts = append(a.hosts[:index], a.hosts[index+1:]...)
	if a.selectedHost == host.Address {
		a.selectedHost = ""
	}
	a.status = "Host removed"
	return nil
}

func (a *app) startServer() {
	if a.server != nil || a.serverBusy {
		return
	}
	a.serverBusy = true
	a.status = "Starting server…"
	go func() {
		fail := func(err error) {
			log.Printf("server failed to start: %v", err)
			a.update(func() {
				a.serverBusy = false
				a.serverEnabled = false
				_ = a.store.SetBool("server_enabled", false)
				a.status = "Server failed: " + err.Error()
			})
		}
		psk, region, err := a.store.ServerParams()
		if err == nil && region == 0 && a.relay == nil {
			// Choosing a relay region takes seconds and may differ between
			// starts, so it is done once and saved: it keeps the address.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			region, err = server.PickRegion(ctx)
			cancel()
			if err == nil {
				err = a.store.SaveRegion(region)
			}
		}
		if err != nil {
			fail(err)
			return
		}
		gate, err := admission.NewGate(a.store.DB(), a.identity.Private(), a.trusted)
		if err != nil {
			fail(err)
			return
		}
		s := &tailcat.Server{Key: a.identity.NodeKey(state.RoleServer), PresharedKey: psk, RegionID: region, Region: a.relay}
		gate.Install(s, func(port uint16) func(net.Conn) {
			if a.serveExtra != nil {
				if h := a.serveExtra(port); h != nil {
					return h
				}
			}
			return a.ports.Handler(port)
		})
		if err := s.Start(); err != nil {
			fail(err)
			return
		}
		runCtx, stop := context.WithCancel(context.Background())
		go gate.Run(runCtx)
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-t.C:
					a.update(func() {
						if a.showSettings {
							a.refreshClients()
						}
					})
				}
			}
		}()
		a.update(func() {
			a.serverBusy = false
			a.server = s
			a.gate, a.stopGate = gate, stop
			a.serverAddr = string(s.TailcatAddr())
			a.status = "Server started"
		})
	}()
}

// serverSwitched runs when the Settings switch changes: it saves the setting
// and starts or stops the server, unless a start or stop is already running.
func (a *app) serverSwitched() {
	if a.serverBusy {
		return
	}
	if err := a.store.SetBool("server_enabled", a.serverEnabled); err != nil {
		a.status = "Saving server setting failed: " + err.Error()
	} else if a.serverEnabled {
		a.startServer()
	} else {
		a.stopServer()
	}
}

func (a *app) stopServer() {
	if a.server == nil || a.serverBusy {
		return
	}
	a.serverBusy = true
	s := a.server
	a.server = nil
	if a.stopGate != nil {
		a.stopGate()
	}
	a.gate, a.stopGate = nil, nil
	a.clients = nil
	a.serverAddr = ""
	a.status = "Stopping server…"
	go func() {
		if err := s.Close(); err != nil {
			a.update(func() {
				a.serverBusy = false
				a.status = "Server stop failed: " + err.Error()
			})
			return
		}
		a.update(func() {
			a.serverBusy = false
			a.status = "Server stopped"
		})
	}()
}
