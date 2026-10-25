package app

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/maximousblk/mochi/internal/connector"
	"github.com/maximousblk/mochi/internal/connector/textsender"
	"github.com/maximousblk/mochi/internal/state"
)

func (a *app) view(c *ui.Context) {
	theme := *c.Theme()
	theme.Font = "Ioskeley Mono"
	c.SetTheme(&theme)
	bar := c.TitleBar()
	ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// The title bar holds the way back to this device's page on the left and
		// the Settings gear on the right. Where the system draws the window
		// controls (macOS), the padding clears them; elsewhere Mochi draws them
		// after the gear, in the same style.
		ui.Row(c).Height(max(bar.Height, 44)).Padding(0, bar.Right+12, 0, bar.Left+16).Background(theme.Background).AlignItems(ui.Center).DragWindow().Children(func() {
			if flatButton(c, "This device", func() {
				ui.Text(c, "Mochi").FontSize(16).Bold()
			}).Clicked() {
				a.goTo(page{})
			}
			ui.Row(c).Grow(1)
			if iconButton(c, settingsIcon, "Settings").Clicked() {
				a.goTo(page{kind: pageSettings})
			}
			a.windowButtons(c)
		})
		ui.Row(c).Height(1).Background(theme.Border)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(240).Background(theme.Surface).Padding(12).Gap(10).AlignItems(ui.Stretch).Children(func() {
				if ui.PrimaryButton(c, "+  Add host").Clicked() {
					a.goTo(page{kind: pageAddHost})
				}
				self := ui.Button(c, "").Label(selfTitle()).AlignItems(ui.Stretch)
				if a.page.kind == pageSelf {
					self.Background(theme.SurfacePressed)
				}
				self.Children(func() {
					ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
						ui.Text(c, selfTitle()).SingleLine()
						ui.Text(c, inboxCaption(len(a.inbox))).FontSize(11).TextColor(theme.TextMuted).SingleLine()
					})
				})
				if self.Clicked() {
					a.goTo(page{})
				}
				ui.Text(c, "Hosts").FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				ui.Scroll(c).Grow(1).Gap(6).AlignItems(ui.Stretch).Children(func() {
					for _, host := range a.hosts {
						item := ui.Button(c, "").Label(host.Name).AlignItems(ui.Stretch)
						if a.page.kind == pageHost && host.Address == a.page.address {
							item.Background(theme.SurfacePressed)
						}
						item.Children(func() {
							ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
								ui.Text(c, host.Name).SingleLine()
								ui.Text(c, host.Address).FontSize(11).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Tooltip(host.Address)
							})
						})
						if item.Clicked() {
							a.goTo(page{kind: pageHost, address: host.Address})
						}
					}
				})
			})

			ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).TrackScroll(&a.pageScroll).Grow(1).Padding(28).Gap(16).AlignItems(ui.Stretch).Children(func() {
					switch a.page.kind {
					case pageSettings:
						a.settingsView(c)
					case pageAddHost:
						a.addHostView(c)
					case pageHost:
						a.hostView(c)
					default:
						a.selfView(c)
					}
				})
				ui.Row(c).Height(28).Background(theme.Surface).Padding(12, 0).AlignItems(ui.Center).Children(func() {
					a.statusBar(c)
				})
			})
		})
	})
}

// goTo shows page p and scrolls it back to the top, so a page never opens part
// way down.
func (a *app) goTo(p page) {
	a.page = p
	a.pageScroll.X, a.pageScroll.Y = 0, 0
}

// statusBar shows the latest status: the name of the page it reports on, which
// links to that page, then the message. Nothing shows before the first status.
func (a *app) statusBar(c *ui.Context) {
	if a.status.text == "" {
		return
	}
	theme := c.Theme()
	label, linked := a.scopeLabel(a.status.scope)
	if linked {
		link := ui.ButtonBase(c).Label("Go to " + label).Cursor(ui.CursorPointer)
		link.Children(func() {
			ui.Text(c, label).TextColor(theme.Accent).SingleLine()
		})
		if link.Clicked() {
			a.goTo(a.status.scope)
		}
	} else {
		ui.Text(c, label).TextColor(theme.TextMuted).SingleLine()
	}
	ui.Text(c, " · ").TextColor(theme.TextMuted).SingleLine()
	ui.Text(c, a.status.text).TextColor(theme.TextMuted).SingleLine()
}

// scopeLabel is the name of the page a status reports on, and whether that page
// can be linked to. A removed host has no page any more, so its status names the
// address, unlinked.
func (a *app) scopeLabel(scope page) (string, bool) {
	switch scope.kind {
	case pageSettings:
		return "Settings", true
	case pageHost:
		for _, host := range a.hosts {
			if host.Address == scope.address {
				return host.Name, true
			}
		}
		return scope.address, false
	default:
		return selfTitle(), true
	}
}

// selfTitle is the name of this machine, which the self page and sidebar show.
// It falls back to "This device" when the machine has no name.
func selfTitle() string {
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "This device"
}

// inboxCaption is the sidebar's line under the self entry: how many texts the
// Inbox holds.
func inboxCaption(n int) string {
	if n == 0 {
		return "This device"
	}
	return fmt.Sprintf("Inbox: %d", n)
}

// selfView is this device's page: its server switch and launchers, its address,
// the Inbox when it holds texts, and the clients waiting for admission.
func (a *app) selfView(c *ui.Context) {
	theme := c.Theme()
	ui.Text(c, selfTitle()).FontSize(28).Bold()
	// The keys sit under the title, styled as a host's address and node key: one
	// muted line each, with the full value in its tooltip and a copy button.
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		pub := a.identity.NodeKey(state.RoleClient).Public().String()
		ui.Text(c, "Client public key: "+pub).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(pub)
		a.copyButton(c, pub, "client public key")
	})
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		id := hex.EncodeToString(a.identity.Public())
		ui.Text(c, "Machine key: "+id).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(id)
		a.copyButton(c, id, "machine key")
	})
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Enable Tailcat server").Grow(1)
		ui.Switch(c, &a.serverEnabled).Disabled(a.busy["server"]).Label("Enable Tailcat server").OnChange(a.serverSwitched)
	})
	ui.Text(c, "Launchers").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, lc := range a.servers {
			name := lc.Name()
			on := a.serving[name]
			warn := on && a.unhealthy[name]
			tip := "Let other machines use " + name
			if launcherTile(c, name, tip, lc.Icon(), on, warn, a.busy[serveOp(name)], func() {
				a.openConfig(serverTarget(lc))
			}).Clicked() {
				a.toggleServing(lc)
			}
		}
	})
	if a.busy["server"] {
		ui.Text(c, "Server operation in progress…").TextColor(theme.TextMuted)
	} else if a.serverAddr != "" {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Server address: "+a.serverAddr).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(a.serverAddr)
			a.copyButton(c, a.serverAddr, "server address")
		})
	}
	a.inboxView(c)
	a.clientsView(c)
}

func (a *app) addHostView(c *ui.Context) {
	ui.Text(c, "Add host").FontSize(24).Bold()
	ui.TextInput(c, &a.nameDraft).Placeholder("Host name").Label("Host name")
	ui.TextInput(c, &a.addrDraft).Placeholder("tc…").Label("Tailcat address")
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "Save host").Disabled(a.busy["host-add"]).Clicked() {
			a.addHost()
		}
		if ui.Button(c, "Cancel").Clicked() {
			a.goTo(page{})
			a.nameDraft, a.addrDraft = "", ""
		}
	})
}

func (a *app) hostView(c *ui.Context) {
	theme := c.Theme()
	var host *state.Host
	for i := range a.hosts {
		if a.hosts[i].Address == a.page.address {
			host = &a.hosts[i]
			break
		}
	}
	if host == nil {
		// The host was removed while its page was open.
		a.goTo(page{})
		return
	}
	h := *host
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, h.Name).FontSize(28).Bold().Grow(1).SingleLine().MinWidth(0)
		if iconButton(c, trashIcon, "Remove host").Clicked() {
			a.openRemove(h)
		}
	})
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, h.Address).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(h.Address)
		a.copyButton(c, h.Address, "connection string")
	})
	if nodeID := nodeIDFromAddress(h.Address); nodeID != "" {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, nodeID).FontSize(12).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(nodeID)
			a.copyButton(c, nodeID, "node ID")
		})
	}

	ui.Text(c, "Launchers").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, lc := range a.connectors {
			name := lc.Name()
			_, connected := a.connections[h.Address][name]
			busy := a.busy[connectOp(h.Address, name)]
			if launcherTile(c, name, "Toggle "+name+" connector", lc.Icon(), connected, false, busy, func() {
				a.openConfig(clientTarget(name, h, lc.ClientFields()))
			}).Clicked() {
				a.toggleConnection(h, lc)
			}
		}
		if launcherTile(c, "Send text", "Send text to this host", textSenderIcon, false, false, false, func() {
			a.openConfig(clientTarget("Send text", h, textsender.ClientFields()))
		}).Clicked() {
			a.openSend(h)
		}
	})
}

// launcherTile is a square launcher: accent-filled while enabled, and yellow
// while enabled but not working (warn). While busy it is disabled and its
// label says so with a "…" suffix. Its right-click menu has a Configure…
// item that calls configure.
func launcherTile(c *ui.Context, name, tooltip string, icon *ui.SVG, enabled, warn, busy bool, configure func()) ui.Element {
	theme := c.Theme()
	fill, text := theme.Accent, theme.AccentText
	if warn {
		fill = theme.Warning
	}
	label := name
	if busy {
		label += "…"
	}
	tile := ui.Button(c, "").Label(name + " launcher").Tooltip(tooltip).Width(104).Height(92).Disabled(busy)
	if enabled {
		tile.Background(fill).TextColor(text)
	}
	tile.Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(5).Children(func() {
			iconEl := ui.Icon(c, icon).Size(28, 28)
			lbl := ui.Text(c, label).FontSize(12)
			if enabled {
				iconEl.TextColor(text)
				lbl.TextColor(text)
			}
		})
	})
	return tile.ContextMenu(func(m *ui.Menu) {
		if m.Item("Configure…").Chosen() {
			configure()
		}
	})
}

func (a *app) copyButton(c *ui.Context, value, label string) {
	if iconButton(c, clipboardIcon, "Copy "+label).Clicked() {
		c.WriteClipboard(value)
		a.say(a.page, "Copied "+label)
	}
}

// windowControls are the window actions the drawn title bar buttons call.
// *mygo.Window has them.
type windowControls interface {
	Minimize()
	Maximize()
	Unmaximize()
	Close()
}

// windowButtons draws minimize, maximize (or restore) and close after the
// Settings gear, when Mochi draws them itself. Close goes through the window's
// OnClose, which hides it to the tray.
func (a *app) windowButtons(c *ui.Context) {
	if a.controls == nil {
		return
	}
	if iconButton(c, minimizeIcon, "Minimize").Clicked() {
		a.controls.Minimize()
	}
	icon, label := maximizeIcon, "Maximize"
	if a.maximized {
		icon, label = restoreIcon, "Restore"
	}
	// MaximizeButton gives it Windows 11's snap layouts on hover.
	if max := iconButton(c, icon, label).MaximizeButton(); max.Clicked() {
		if a.maximized {
			a.controls.Unmaximize()
		} else {
			a.controls.Maximize()
		}
	}
	if iconButton(c, closeIcon, "Close").Clicked() {
		a.controls.Close()
	}
}

// iconButton is a flat square icon button.
func iconButton(c *ui.Context, icon *ui.SVG, tooltip string) ui.Element {
	t := c.Theme()
	return flatButton(c, tooltip, func() {
		ui.Icon(c, icon).Size(18, 18).TextColor(t.TextMuted)
	}).Width(32).Height(32)
}

// flatButton is a button with no face or border: its background darkens
// only while hovered, a step further while pressed.
func flatButton(c *ui.Context, tooltip string, content func()) ui.Element {
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
	info, err := connector.ParseHost(address)
	if err != nil {
		return ""
	}
	return info.ServerPublic.NodePublic.String()
}

// settingsView holds what is about this machine and not about any one host: the
// database, the log file, and the certificates. The keys, the server switch and
// the admission lists are on the self page.
func (a *app) settingsView(c *ui.Context) {
	theme := c.Theme()
	ui.Text(c, "Settings").FontSize(24).Bold()
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		dbPath := a.store.DataDir()
		ui.Text(c, "Database: "+dbPath).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(dbPath)
		if iconButton(c, folderIcon, "Open containing folder").Clicked() {
			a.openContaining(dbPath)
		}
	})
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		logPath := a.logPath
		ui.Text(c, "Log file: "+logPath).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(logPath)
		if iconButton(c, folderIcon, "Open containing folder").Clicked() {
			a.openContaining(logPath)
		}
	})
	ui.Text(c, "Certificates").FontSize(18).Bold()
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.TextInput(c, &a.certDraft).Placeholder("Paste a certificate").Label("Certificate").Grow(1)
		if ui.Button(c, "Add").Disabled(a.busy["cert-add"]).Clicked() && a.certDraft != "" {
			a.addCert(a.certDraft)
		}
	})
}

// openContaining opens the folder that holds path in the file manager. Opening a
// folder starts a file manager, which can take a moment, so it runs off the UI
// thread. A failure is reported on the Settings page, where both folder buttons are.
func (a *app) openContaining(path string) {
	open := mygo.Shell.OpenPath
	if a.openPath != nil {
		open = a.openPath
	}
	dir := filepath.Dir(path)
	a.async("open-folder", func() error {
		return open(dir)
	}, func(err error) {
		if err != nil {
			a.say(page{kind: pageSettings}, "Opening folder failed: "+err.Error())
		}
	})
}

// addCert saves a certificate pasted as hex. The hex check is pure and runs
// here; the store write runs off the UI thread.
func (a *app) addCert(text string) {
	cert, err := hex.DecodeString(text)
	if err != nil {
		a.say(page{kind: pageSettings}, "Certificate is not valid hex")
		return
	}
	a.async("cert-add", func() error {
		return a.store.AddCert(cert)
	}, func(err error) {
		if err != nil {
			a.say(page{kind: pageSettings}, "Saving certificate failed: "+err.Error())
			return
		}
		a.certDraft = ""
		a.say(page{kind: pageSettings}, "Certificate saved")
	})
}
