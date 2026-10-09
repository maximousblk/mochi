// Package app is Mochi's window: the host list, the launchers, Settings, and the
// tray icon that keeps the app running while the window is closed.
package app

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/connector/files"
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
	window        *mygo.Window
	tray          *mygo.Tray
	quitting      bool

	// Seams for tests; all nil in the real app.
	runOnUI    func(func())
	relay      *tailcfg.DERPRegion              // serve through this relay instead of picking a public one
	openPath   func(string) error               // opens a folder; mygo.Shell.OpenPath when nil
	serveExtra func(port uint16) func(net.Conn) // more ports for the server to serve
}

// Run starts the app and blocks until it exits. args are the command-line
// arguments after the program name.
func Run(args []string) error {
	if err := registerFonts(); err != nil {
		return fmt.Errorf("registering Ioskeley Mono: %w", err)
	}
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
	})
	mygo.App.OnWindowAllClosed(func() {})
	return mygo.App.Run()
}

// load reads everything the app starts from out of store: hosts, the server
// setting, this machine's identity, the trusted keys and the connectors.
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
	sshConfigPath, err := store.StringSetting("ssh_config_path")
	if err != nil || sshConfigPath == "" {
		if sshConfigPath, err = ssh.DefaultConfigPath(); err != nil {
			return err
		}
	}
	a.connectors = newConnectors(sshConfigPath, a.dialing)
	for _, c := range a.connectors {
		c.SetHosts(a.hosts)
	}
	// The WebDAV switch used to control the files service, which is now the
	// Files connector's switch. Move the stored value across, and drop the old
	// row, so the new WebDAV server does not inherit it and start on.
	if on, err := store.BoolSetting("connector_server:webdav"); err == nil {
		if err := store.SetBool("connector_server:files", on); err != nil {
			return fmt.Errorf("moving the files setting: %w", err)
		}
		if err := store.DeleteSetting("connector_server:webdav"); err != nil {
			return fmt.Errorf("moving the files setting: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("loading the WebDAV setting: %w", err)
	}
	for _, c := range a.connectors {
		on, err := store.BoolSetting(connector.SettingKey(c))
		switch {
		case err == nil:
			c.SetServerEnabled(on)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("loading %s server setting: %w", c.Name(), err)
		}
	}
	if sshConnector, ok := a.connectors[0].(*ssh.Connector); ok {
		if err := sshConnector.Refresh(a.hosts); err != nil {
			log.Printf("checking SSH config: %v", err)
		}
	}
	if len(a.hosts) > 0 {
		a.selectedHost = a.hosts[0].Address
	}
	return nil
}

// newConnectors returns the launchers in the order the UI shows them.
func newConnectors(sshConfigPath string, dialing admission.Dialing) []connector.Connector {
	return []connector.Connector{
		ssh.New(sshConfigPath, sshIcon),
		files.New(folderIcon, dialing),
		webdav.New(webdavIcon, dialing),
		rustdesk.New(rustdeskIcon, dialing),
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

func (a *app) showWindow() {
	if a.window == nil {
		return
	}
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
	var cleanupErrors []error
	for _, c := range a.connectors {
		if err := c.Disable(host); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", c.Name(), err))
		}
	}
	if err := errors.Join(cleanupErrors...); err != nil {
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

// serverHandler returns the handler for a Tailcat service port, or nil if no
// enabled connector offers it. It is looked up per connection, so switching a
// connector takes effect without restarting the server.
func (a *app) serverHandler(s *tailcat.Server, port uint16) func(net.Conn) {
	sv := &connector.ServerServices{Serve: map[uint16]func(net.Conn){}}
	for _, c := range a.connectors {
		if c.ServerEnabled() {
			c.ServerServe(sv)
		}
	}
	if port == 22 && (sv.Shell || sv.Files != nil) {
		return s.SSHConnHandler(tailcat.SSHOptions{Shell: sv.Shell, Files: sv.Files})
	}
	if h, ok := sv.Serve[port]; ok {
		return h
	}
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
			return a.serverHandler(s, port)
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
