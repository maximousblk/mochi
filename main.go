package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

type Host struct {
	Name    string
	Address string
}

type app struct {
	hosts         []Host
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
	gate          *Gate
	stopGate      context.CancelFunc
	identity      Identity
	dialing       Dialing
	trusted       []ed25519.PublicKey
	certDraft     string
	clients       map[string][]Client
	store         *StateStore
	lastTrayClick time.Time // when the tray icon was last clicked, for double clicks
	// Seams for tests; all nil in the real app.
	runOnUI    func(func())
	relay      *tailcfg.DERPRegion              // serve through this relay instead of picking a public one
	openPath   func(string) error               // opens a folder; mygo.Shell.OpenPath when nil
	serveExtra func(port uint16) func(net.Conn) // more ports for the server to serve
	connectors []Connector
	window     *mygo.Window
	tray       *mygo.Tray
	quitting   bool
}

//go:embed resources/tray.png
var trayIcon []byte

//go:embed resources/ssh.svg
var sshIconData []byte

var sshIcon = ui.MustParseSVG(sshIconData)

//go:embed resources/trash.svg
var trashIconData []byte

var trashIcon = ui.MustParseSVG(trashIconData)

//go:embed resources/clipboard.svg
var clipboardIconData []byte

var clipboardIcon = ui.MustParseSVG(clipboardIconData)

//go:embed resources/folder.svg
var folderIconData []byte

var folderIcon = ui.MustParseSVG(folderIconData)

//go:embed resources/fonts/IoskeleyMono-Regular.ttf
var ioskeleyRegular []byte

//go:embed resources/fonts/IoskeleyMono-Medium.ttf
var ioskeleyMedium []byte

//go:embed resources/fonts/IoskeleyMono-SemiBold.ttf
var ioskeleySemiBold []byte

//go:embed resources/fonts/IoskeleyMono-Bold.ttf
var ioskeleyBold []byte

func registerFonts() error {
	for _, face := range [][]byte{ioskeleyRegular, ioskeleyMedium, ioskeleySemiBold, ioskeleyBold} {
		if err := ui.RegisterFont(face, "Ioskeley Mono"); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--verbose" {
			webdavVerbose = true
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "issue" {
		if err := runIssue(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "mochi:", err)
			os.Exit(1)
		}
		return
	}
	connectors := newConnectors("", Dialing{})
	if len(os.Args) > 1 {
		for _, connector := range connectors {
			if os.Args[1] != connector.CLICommand() {
				continue
			}
			state, err := loadClientState()
			if err != nil {
				fmt.Fprintln(os.Stderr, "mochi:", err)
				os.Exit(1)
			}
			connector.SetHosts(state.hosts)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			dialing := Dialing{Key: state.identity.NodeKey(RoleClient), Certs: func() [][]byte { return state.certs }}
			err = connector.RunCommand(ctx, os.Args[2:], os.Stdin, os.Stdout, dialing)
			stop()
			if err != nil {
				fmt.Fprintln(os.Stderr, "mochi:", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := registerFonts(); err != nil {
		log.Fatalf("registering Ioskeley Mono: %v", err)
	}

	a := &app{}
	statePath, err := statePath()
	if err != nil {
		log.Fatal(err)
	}
	legacyPath, err := legacyConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenState(statePath, legacyPath)
	if err != nil {
		log.Fatalf("opening state: %v", err)
	}
	if err := a.load(store); err != nil {
		log.Fatal(err)
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
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *app) view(c *ui.Context) {
	theme := *c.Theme()
	theme.Font = "Ioskeley Mono"
	c.SetTheme(&theme)
	bar := c.TitleBar()
	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Height(max(bar.Height, 44)).Padding(0, bar.Right+12, 0, bar.Left+16).Background(theme.Background).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, "Mochi").FontSize(16).Bold().Grow(1)
		})
		ui.Row(c).Height(1).Background(theme.Border)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(240).Background(theme.Surface).Padding(12).Gap(10).AlignItems(ui.Stretch).Children(func() {
				if ui.PrimaryButton(c, "+  Add host").Clicked() {
					a.addingHost = true
					a.showSettings = false
				}
				ui.Text(c, "Hosts").FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				ui.Scroll(c).Grow(1).Gap(6).AlignItems(ui.Stretch).Children(func() {
					for _, host := range a.hosts {
						item := ui.Button(c, "").Label(host.Name).AlignItems(ui.Stretch)
						if host.Address == a.selectedHost {
							item.Background(theme.SurfacePressed)
						}
						item.Children(func() {
							ui.Column(c).AlignItems(ui.Stretch).Children(func() {
								ui.Text(c, host.Name).SingleLine()
								ui.Text(c, host.Address).FontSize(11).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Tooltip(host.Address)
							})
						})
						if item.Clicked() {
							a.selectedHost = host.Address
							a.showSettings = false
							a.addingHost = false
						}
					}
				})
				if ui.Button(c, "⚙  Settings").Clicked() {
					a.showSettings = true
					a.addingHost = false
				}
			})

			ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Grow(1).Padding(28).Gap(16).AlignItems(ui.Stretch).Children(func() {
					switch {
					case a.showSettings:
						a.settingsView(c)
					case a.addingHost:
						a.addHostView(c)
					default:
						a.hostView(c)
					}
				})
				ui.Row(c).Height(28).Background(theme.Surface).Padding(12, 0).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.status).TextColor(theme.TextMuted).SingleLine()
				})
			})
		})
	})
}

func (a *app) addHostView(c *ui.Context) {
	ui.Text(c, "Add host").FontSize(24).Bold()
	ui.TextInput(c, &a.nameDraft).Placeholder("Host name").Label("Host name")
	ui.TextInput(c, &a.addrDraft).Placeholder("tc…").Label("Tailcat address")
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "Save host").Clicked() {
			a.addHost()
		}
		if ui.Button(c, "Cancel").Clicked() {
			a.addingHost = false
			a.nameDraft, a.addrDraft = "", ""
		}
	})
}

func (a *app) hostView(c *ui.Context) {
	theme := c.Theme()
	var host *Host
	if a.selectedHost == "" {
		ui.Text(c, "Mochi").FontSize(28).Bold()
		ui.Text(c, "Select a host from the sidebar or add a new one.").TextColor(theme.TextMuted)
		return
	} else {
		for i := range a.hosts {
			if a.hosts[i].Address == a.selectedHost {
				host = &a.hosts[i]
				break
			}
		}
		if host == nil {
			a.selectedHost = ""
			return
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, host.Name).FontSize(28).Bold().Grow(1).SingleLine().MinWidth(0)
			bin := iconButton(c, trashIcon, "Remove host")
			if bin.Clicked() {
				a.confirmRemove = true
			}
		})
		if ui.AlertDialog(c, &a.confirmRemove, "Remove "+host.Name+"?", "This removes the host and its SSH config entry.", "No", "Yes") == 1 {
			a.confirmRemove = false
			for i := range a.hosts {
				if a.hosts[i].Address == a.selectedHost {
					if err := a.removeHost(i); err != nil {
						a.status = "Host cleanup failed: " + err.Error()
					}
					break
				}
			}
			return
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, host.Address).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(host.Address)
			a.copyButton(c, host.Address, "connection string")
		})
		if nodeID := nodeIDFromAddress(host.Address); nodeID != "" {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, nodeID).FontSize(12).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(nodeID)
				a.copyButton(c, nodeID, "node ID")
			})
		}
	}

	ui.Text(c, "Launchers").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, connector := range a.connectors {
			enabled := connector.Enabled(*host)
			if launcherTile(c, connector.Name(), "Toggle "+connector.Name()+" connector", connector.Icon(), enabled).Clicked() {
				var err error
				if enabled {
					err = connector.Disable(*host)
				} else {
					err = connector.Enable(*host)
				}
				if err != nil {
					a.status = connector.Name() + " connector failed: " + err.Error()
				} else if u, ok := connector.(interface{ URL(Host) string }); ok && connector.Enabled(*host) {
					c.WriteClipboard(u.URL(*host))
					a.status = connector.Name() + " address copied: " + u.URL(*host)
				} else if connector.Enabled(*host) {
					a.status = connector.Name() + " enabled"
				} else {
					a.status = connector.Name() + " disabled"
				}
			}
		}
		if launcherTile(c, "Send text", "Send text to this host", textSenderIcon, false).Clicked() {
			a.sendOpen = true
		}
	})
	a.sendDialog(c, *host)
}

// launcherTile is a square launcher: accent-filled while enabled.
func launcherTile(c *ui.Context, name, tooltip string, icon *ui.SVG, enabled bool) *ui.Element {
	theme := c.Theme()
	tile := ui.Button(c, "").Label(name + " launcher").Tooltip(tooltip).Width(104).Height(92)
	if enabled {
		tile.Background(theme.Accent).TextColor(theme.AccentText)
	}
	tile.Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(5).Children(func() {
			iconEl := ui.Icon(c, icon).Size(28, 28)
			label := ui.Text(c, name).FontSize(12)
			if enabled {
				iconEl.TextColor(theme.AccentText)
				label.TextColor(theme.AccentText)
			}
		})
	})
	return tile
}

func (a *app) copyButton(c *ui.Context, value, label string) {
	if iconButton(c, clipboardIcon, "Copy "+label).Clicked() {
		c.WriteClipboard(value)
		a.status = "Copied " + label
	}
}

// iconButton is a flat square icon button.
func iconButton(c *ui.Context, icon *ui.SVG, tooltip string) *ui.Element {
	t := c.Theme()
	return flatButton(c, tooltip, func() {
		ui.Icon(c, icon).Size(18, 18).TextColor(t.TextMuted)
	}).Width(32).Height(32)
}

// flatButton is a button with no face or border: its background darkens
// only while hovered, a step further while pressed.
func flatButton(c *ui.Context, tooltip string, content func()) *ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Radius(t.Radius).Padding(t.Space(1), t.Space(1.5)).Tooltip(tooltip).Label(tooltip)
	b.Children(content)
	b.Background(ui.Transparent)
	if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	return b
}

// nodeIDFromAddress returns the Tailcat server node key that the address
// encodes, or "" if the address does not parse.
func nodeIDFromAddress(address string) string {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return ""
	}
	return info.ServerPublic.NodePublic.String()
}

func (a *app) settingsView(c *ui.Context) {
	theme := c.Theme()
	ui.Text(c, "Settings").FontSize(24).Bold()
	changed := false
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Enable Tailcat server").Grow(1)
		changed = ui.Switch(c, &a.serverEnabled).Disabled(a.serverBusy).Label("Enable Tailcat server").Changed()
	})
	if changed && !a.serverBusy {
		if err := a.store.SetBool("server_enabled", a.serverEnabled); err != nil {
			a.status = "Saving server setting failed: " + err.Error()
		} else if a.serverEnabled {
			a.startServer()
		} else {
			a.stopServer()
		}
	}
	ui.Text(c, "Connectors").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, connector := range a.connectors {
			on := connector.ServerEnabled()
			if launcherTile(c, connector.Name(), "Let other machines use "+connector.Name(), connector.Icon(), on).Clicked() {
				connector.SetServerEnabled(!on)
				if err := a.store.SetBool(serverSettingKey(connector), !on); err != nil {
					a.status = "Saving " + connector.Name() + " setting failed: " + err.Error()
				}
			}
		}
	})
	if a.serverBusy {
		ui.Text(c, "Server operation in progress…").TextColor(theme.TextMuted)
	} else if a.serverAddr != "" {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Server address: "+a.serverAddr).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(a.serverAddr)
			a.copyButton(c, a.serverAddr, "server address")
		})
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "Database: "+a.store.path).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(a.store.path)
		if iconButton(c, folderIcon, "Open containing folder").Clicked() {
			open := mygo.Shell.OpenPath
			if a.openPath != nil {
				open = a.openPath
			}
			if err := open(filepath.Dir(a.store.path)); err != nil {
				a.status = "Opening folder failed: " + err.Error()
			}
		}
	})
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		pub := a.identity.NodeKey(RoleClient).Public().String()
		ui.Text(c, "Client public key: "+pub).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(pub)
		a.copyButton(c, pub, "client public key")
	})
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		id := hex.EncodeToString(a.identity.Public())
		ui.Text(c, "Machine key: "+id).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(id)
		a.copyButton(c, id, "machine key")
	})
	ui.Text(c, "Certificates").FontSize(18).Bold()
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.TextInput(c, &a.certDraft).Placeholder("Paste a certificate").Label("Certificate").Grow(1)
		if ui.Button(c, "Add").Clicked() && a.certDraft != "" {
			if cert, err := hex.DecodeString(a.certDraft); err != nil {
				a.status = "Certificate is not valid hex"
			} else if err := a.store.AddCert(cert); err != nil {
				a.status = "Saving certificate failed: " + err.Error()
			} else {
				a.certDraft = ""
				a.status = "Certificate saved"
			}
		}
	})
	a.clientsView(c)
}

// load reads everything the app starts from out of store: hosts, the server
// setting, this machine's identity, the trusted keys and the connectors.
func (a *app) load(store *StateStore) (err error) {
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
	if a.trusted, err = TrustedKeys(); err != nil {
		return fmt.Errorf("loading trusted keys: %w", err)
	}
	a.dialing = Dialing{Key: a.identity.NodeKey(RoleClient), Certs: func() [][]byte {
		certs, _ := store.Certs()
		return certs
	}}
	sshConfigPath, err := store.StringSetting("ssh_config_path")
	if err != nil || sshConfigPath == "" {
		if sshConfigPath, err = defaultSSHConfigPath(); err != nil {
			return err
		}
	}
	a.connectors = newConnectors(sshConfigPath, a.dialing)
	for _, connector := range a.connectors {
		connector.SetHosts(a.hosts)
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
	for _, connector := range a.connectors {
		on, err := store.BoolSetting(serverSettingKey(connector))
		switch {
		case err == nil:
			connector.SetServerEnabled(on)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("loading %s server setting: %w", connector.Name(), err)
		}
	}
	if err := a.connectors[0].(*SSHConfigConnector).Refresh(a.hosts); err != nil {
		log.Printf("checking SSH config: %v", err)
	}
	if len(a.hosts) > 0 {
		a.selectedHost = a.hosts[0].Address
	}
	return nil
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
	a.hosts = append(a.hosts, Host{Name: a.nameDraft, Address: a.addrDraft})
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
	for _, connector := range a.connectors {
		if err := connector.Disable(host); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", connector.Name(), err))
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
	sv := &ServerServices{Serve: map[uint16]func(net.Conn){}}
	for _, connector := range a.connectors {
		if connector.ServerEnabled() {
			connector.ServerServe(sv)
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
			region, err = PickRegion(ctx)
			cancel()
			if err == nil {
				err = a.store.SaveRegion(region)
			}
		}
		if err != nil {
			fail(err)
			return
		}
		gate, err := NewGate(a.store.db, a.identity.Private(), a.trusted)
		if err != nil {
			fail(err)
			return
		}
		s := &tailcat.Server{Key: a.identity.NodeKey(RoleServer), PresharedKey: psk, RegionID: region, Region: a.relay}
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
