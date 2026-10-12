package app

import (
	"encoding/hex"
	"path/filepath"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

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
				ui.Scroll(c).TrackScroll(&a.pageScroll).Grow(1).Padding(28).Gap(16).AlignItems(ui.Stretch).Children(func() {
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
	var host *state.Host
	if a.selectedHost == "" {
		ui.Text(c, "Mochi").FontSize(28).Bold()
		ui.Text(c, "Select a host from the sidebar or add a new one.").TextColor(theme.TextMuted)
		return
	}
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
		if iconButton(c, trashIcon, "Remove host").Clicked() {
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

	ui.Text(c, "Launchers").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, lc := range a.connectors {
			enabled := lc.Enabled(*host)
			if launcherTile(c, lc.Name(), "Toggle "+lc.Name()+" connector", lc.Icon(), enabled).Clicked() {
				var err error
				if enabled {
					err = lc.Disconnect(*host)
				} else {
					err = lc.Connect(*host)
				}
				if err != nil {
					a.status = lc.Name() + " connector failed: " + err.Error()
				} else if u, ok := lc.(interface{ URL(state.Host) string }); ok && lc.Enabled(*host) {
					c.WriteClipboard(u.URL(*host))
					a.status = lc.Name() + " address copied: " + u.URL(*host)
				} else if lc.Enabled(*host) {
					a.status = lc.Name() + " enabled"
				} else {
					a.status = lc.Name() + " disabled"
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
func launcherTile(c *ui.Context, name, tooltip string, icon *ui.SVG, enabled bool) ui.Element {
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
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return ""
	}
	return info.ServerPublic.NodePublic.String()
}

func (a *app) settingsView(c *ui.Context) {
	theme := c.Theme()
	ui.Text(c, "Settings").FontSize(24).Bold()
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Enable Tailcat server").Grow(1)
		ui.Switch(c, &a.serverEnabled).Disabled(a.serverBusy).Label("Enable Tailcat server").OnChange(a.serverSwitched)
	})
	ui.Text(c, "Connectors").FontSize(18).Bold()
	ui.Row(c).Gap(12).Children(func() {
		for _, lc := range a.connectors {
			on := lc.ServerEnabled()
			if launcherTile(c, lc.Name(), "Let other machines use "+lc.Name(), lc.Icon(), on).Clicked() {
				var err error
				if on {
					err = lc.StopListening()
				} else {
					err = lc.Listen()
				}
				if err != nil {
					a.status = lc.Name() + " server not changed: " + err.Error()
				} else if err := a.store.SetBool(connector.SettingKey(lc), !on); err != nil {
					a.status = "Saving " + lc.Name() + " setting failed: " + err.Error()
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
		dbPath := a.store.DatabasePath()
		ui.Text(c, "Database: "+dbPath).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1).Tooltip(dbPath)
		if iconButton(c, folderIcon, "Open containing folder").Clicked() {
			open := mygo.Shell.OpenPath
			if a.openPath != nil {
				open = a.openPath
			}
			if err := open(filepath.Dir(dbPath)); err != nil {
				a.status = "Opening folder failed: " + err.Error()
			}
		}
	})
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
