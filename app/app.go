// Package app is Mochi's window: this device's page, the host pages, the
// launchers, the dialogs that open in their own windows, and the tray icon that
// keeps the app running while the window is closed.
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

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/maximousblk/mochi/internal/admission"
	"github.com/maximousblk/mochi/internal/connector"
	"github.com/maximousblk/mochi/internal/connector/rustdesk"
	"github.com/maximousblk/mochi/internal/connector/ssh"
	"github.com/maximousblk/mochi/internal/connector/textsender"
	"github.com/maximousblk/mochi/internal/connector/webdav"
	"github.com/maximousblk/mochi/internal/server"
	"github.com/maximousblk/mochi/internal/state"
	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

// pageKind is which page the main window shows. The zero value is the self page,
// which is the page the app opens on.
type pageKind int

const (
	pageSelf pageKind = iota
	pageSettings
	pageAddHost
	pageHost
)

// page is the main window's current page. address is the host a pageHost shows.
type page struct {
	kind    pageKind
	address string
}

type app struct {
	hosts         []state.Host
	page          page
	nameDraft     string
	addrDraft     string
	status        status
	logPath       string
	serverAddr    string
	serverEnabled bool
	server        *tailcat.Server
	gate          *admission.Gate
	stopGate      context.CancelFunc
	identity      state.Identity
	dialing       admission.Dialing
	trusted       []ed25519.PublicKey
	certDraft     string
	clients       map[string][]admission.Client
	// clientsReload runs the reads of clients off the UI thread, one at a time,
	// with a read after the last request so the lists catch up with a change.
	clientsReload reload
	store         *state.StateStore
	lastTrayClick time.Time // when the tray icon was last clicked, for double clicks
	connectors    []connector.Connector
	// servers is every launcher's server side, in the order the Settings page
	// shows them: the connectors, then the Send text receiver.
	servers  []connector.Server
	ports    *connector.Ports
	window   *mygo.Window
	tray     *mygo.Tray
	quitting bool

	// inbox is the saved texts, newest first, as the self page's Inbox shows them.
	// inboxReload runs their reads the same way clientsReload does.
	inbox       []state.ReceivedText
	inboxReload reload

	// serving is each launcher's server-side switch, by name, as the app last
	// set it. connections holds the clients open now, by host address and then
	// by launcher name. Clients are not saved: they exist only while open.
	serving     map[string]bool
	connections map[string]map[string]connector.Client

	// busy holds the operations running off the UI thread, by busy key. The view
	// shows an operation as busy and starts nothing new under the same key.
	busy map[string]bool

	// dialogs holds the open dialog windows, by dialog key. Each dialog has its
	// own window, so the main window stays usable while they are open.
	dialogs map[string]dialog

	// pageScroll is where the content scrolls, so the app can bring it back to
	// the top when the page changes.
	pageScroll ui.ScrollState

	// unhealthy names the launchers that are not healthy, as of the last check.
	// Healthy may dial, so the view reads this instead of calling it on every
	// frame. The view draws a launcher yellow only while its switch is on.
	unhealthy map[string]bool

	// Seams for tests; all nil in the real app.
	runOnUI    func(func())
	relay      *tailcfg.DERPRegion                  // serve through this relay instead of picking a public one
	openPath   func(string) error                   // opens a folder; mygo.Shell.OpenPath when nil
	serveExtra func(port uint16) func(net.Conn)     // more ports for the server to serve
	copyText   func(string)                         // copies text; mygo's clipboard when nil
	notify     func(mygo.NotificationOptions) error // shows a notification; mygo's when nil
	openWindow func(title string, w, h int, view func(*ui.Context)) dialogWindow
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
	// the database. The Settings page shows this path. If the file cannot be
	// opened, logging stays on stderr.
	logPath := filepath.Join(filepath.Dir(statePath), "mochi.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	a := &app{logPath: logPath}
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
	// The click is registered before Run so that a click which launches the app
	// is delivered too.
	mygo.App.OnNotificationClick(a.notificationClicked)
	return mygo.App.Run()
}

// load reads everything the app starts from out of store: hosts, the server
// setting, this machine's identity, the trusted keys and the launchers. It runs
// before the window opens, so it may read and listen synchronously.
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
		a.servers = append(a.servers, c)
	}
	a.servers = append(a.servers, textsender.NewReceiver(a.ports, textSenderIcon, a.canNotify, a.receiveText))
	if a.inbox, err = store.ReceivedTexts(); err != nil {
		return fmt.Errorf("loading inbox: %w", err)
	}
	for _, c := range a.servers {
		on, err := store.BoolSetting(connector.SettingKey(c))
		switch {
		case err == nil && on:
			cfg, cerr := a.loadConfig(connector.ServerConfigKey(c), c.ServerFields())
			if cerr != nil {
				a.say(page{}, c.Name()+" settings not loaded, using defaults: "+cerr.Error())
			}
			if err := c.Listen(cfg); err != nil {
				a.say(page{}, c.Name()+" server not started: "+err.Error())
			} else {
				a.serving[c.Name()] = true
			}
		case err == nil:
			if err := c.Stop(); err != nil {
				a.say(page{}, c.Name()+" server not stopped: "+err.Error())
			}
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("loading %s server setting: %w", c.Name(), err)
		}
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

// update runs fn on the UI thread, then redraws every open window. The dialogs
// read the same state as the main window, so each one is redrawn too. Tests set
// runOnUI to run fn where they can step the view; with neither it is the
// window's.
func (a *app) update(fn func()) {
	redraw := func() {
		fn()
		for _, d := range a.dialogs {
			d.win.Invalidate()
		}
	}
	if a.runOnUI != nil {
		a.runOnUI(redraw)
		return
	}
	a.window.Update(redraw)
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
	for _, c := range a.servers {
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

// serveOp is the busy key of turning the server side of launcher name on or off.
func serveOp(name string) string { return "serve|" + name }

// toggleServing turns a launcher's server side on or off, and saves its switch.
// Listening reads the launcher's config and may bind a port, so it runs off the
// UI thread as the launcher's busy operation.
func (a *app) toggleServing(lc connector.Server) {
	name := lc.Name()
	on := a.serving[name]
	key := connector.ServerConfigKey(lc)
	var warn, saveErr error
	a.async(serveOp(name), func() error {
		if on {
			if err := lc.Stop(); err != nil {
				return err
			}
		} else {
			cfg, err := a.loadConfig(key, lc.ServerFields())
			warn = err
			if err := lc.Listen(cfg); err != nil {
				return err
			}
		}
		saveErr = a.store.SetBool(connector.SettingKey(lc), !on)
		return nil
	}, func(err error) {
		if warn != nil {
			a.say(page{}, name+" settings not loaded, using defaults: "+warn.Error())
		}
		if err != nil {
			a.say(page{}, name+" server not changed: "+err.Error())
			return
		}
		a.serving[name] = !on
		if saveErr != nil {
			a.say(page{}, "Saving "+name+" setting failed: "+saveErr.Error())
		}
		a.checkHealth()
	})
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

// exit quits the app. The dialog windows close with it, and the connections
// are disconnected before the store closes.
func (a *app) exit() {
	a.quitting = true
	a.closeAllDialogs()
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

// addHost saves the host typed on the Add host page, off the UI thread, then
// shows the new host's page.
func (a *app) addHost() {
	if a.busy["host-add"] {
		return
	}
	if a.nameDraft == "" || a.addrDraft == "" {
		a.say(page{}, "Host name and address are required")
		return
	}
	if _, err := tailcat.ParseAddr(tailcat.Addr(a.addrDraft)); err != nil {
		a.say(page{}, fmt.Sprintf("Invalid Tailcat address: %v", err))
		return
	}
	host := state.Host{Name: a.nameDraft, Address: a.addrDraft}
	a.say(hostScope(host.Address), "Adding host…")
	a.async("host-add", func() error {
		return a.store.AddHost(host)
	}, func(err error) {
		if err != nil {
			a.say(hostScope(host.Address), fmt.Sprintf("Host not added: %v", err))
			return
		}
		a.hosts = append(a.hosts, host)
		a.page = page{kind: pageHost, address: host.Address}
		a.nameDraft, a.addrDraft = "", ""
		a.say(hostScope(host.Address), "Host added")
	})
}

// removeHost removes the host at address. It runs off the UI thread, after the
// host's connections are closed: the connections are detached here and
// disconnected there. failed runs on the UI thread only when the removal fails,
// so the Remove dialog can show why. On success the host's dialogs are closed,
// because their targets no longer exist.
func (a *app) removeHost(address string, failed func(error)) {
	op := hostRemoveOp(address)
	if a.busy[op] {
		return
	}
	clients := a.detachHost(address)
	a.async(op, func() error {
		var errs []error
		for _, client := range clients {
			if err := client.Disconnect(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := errors.Join(errs...); err != nil {
			return fmt.Errorf("disconnect host: %w", err)
		}
		if err := a.store.DeleteHost(address); err != nil {
			return fmt.Errorf("delete host from state: %w", err)
		}
		return a.deleteClientConfigs(address)
	}, func(err error) {
		if err != nil {
			a.say(hostScope(address), "Remove failed: "+err.Error())
			if failed != nil {
				failed(err)
			}
			return
		}
		for i, h := range a.hosts {
			if h.Address == address {
				a.hosts = append(a.hosts[:i], a.hosts[i+1:]...)
				break
			}
		}
		if a.page.kind == pageHost && a.page.address == address {
			a.page = page{}
		}
		a.closeHostDialogs(address)
		a.say(page{}, "Host removed")
	})
}

// launchServer starts the Tailcat server and its admission gate. It does the
// blocking work, so it runs off the UI thread and touches no app state.
func (a *app) launchServer() (*tailcat.Server, *admission.Gate, context.CancelFunc, error) {
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
		return nil, nil, nil, err
	}
	gate, err := admission.NewGate(a.store.DB(), a.identity.Private(), a.trusted)
	if err != nil {
		return nil, nil, nil, err
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
		return nil, nil, nil, err
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
					if a.page.kind == pageSelf {
						a.reloadClients()
					}
				})
			}
		}
	}()
	return s, gate, stop, nil
}

// startServer starts the server off the UI thread, as the server's busy
// operation. A start that fails turns the server switch back off.
func (a *app) startServer() {
	if a.server != nil || a.busy["server"] {
		return
	}
	a.say(page{}, "Starting server…")
	var s *tailcat.Server
	var gate *admission.Gate
	var stop context.CancelFunc
	a.async("server", func() error {
		var err error
		s, gate, stop, err = a.launchServer()
		if err != nil {
			log.Printf("server failed to start: %v", err)
			_ = a.store.SetBool("server_enabled", false)
		}
		return err
	}, func(err error) {
		if err != nil {
			a.serverEnabled = false
			a.say(page{}, "Server failed: "+err.Error())
			return
		}
		a.server = s
		a.gate, a.stopGate = gate, stop
		a.serverAddr = string(s.TailcatAddr())
		a.say(page{}, "Server started")
		a.reloadClients()
	})
}

// serverSwitched runs when the server switch changes: it saves the setting off
// the UI thread, then starts or stops the server to match the switch as it is
// when the save finishes, so the last flip wins.
func (a *app) serverSwitched() {
	if a.busy["server"] {
		return
	}
	on := a.serverEnabled
	go func() {
		err := a.store.SetBool("server_enabled", on)
		a.update(func() {
			if err != nil {
				a.say(page{}, "Saving server setting failed: "+err.Error())
				return
			}
			if a.serverEnabled {
				a.startServer()
			} else {
				a.stopServer()
			}
		})
	}()
}

// stopServer stops the server. The gate stops at once, so no client is admitted
// while the server closes; the close itself runs off the UI thread.
func (a *app) stopServer() {
	if a.server == nil || a.busy["server"] {
		return
	}
	s := a.server
	a.server = nil
	if a.stopGate != nil {
		a.stopGate()
	}
	a.gate, a.stopGate = nil, nil
	a.clients = nil
	a.serverAddr = ""
	a.say(page{}, "Stopping server…")
	a.async("server", func() error {
		return s.Close()
	}, func(err error) {
		if err != nil {
			a.say(page{}, "Server stop failed: "+err.Error())
			return
		}
		a.say(page{}, "Server stopped")
	})
}

// status is the latest message in the status bar. scope is the page the message
// reports on, which the bar links to; text is what follows the scope's label.
type status struct {
	scope page
	text  string
}

// say sets the status bar to text, reported on scope. Every status goes through
// here so that the bar always knows which page the message belongs to.
func (a *app) say(scope page, text string) {
	a.status = status{scope: scope, text: text}
}
