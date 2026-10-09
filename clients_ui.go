package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// refreshClients reloads the lists Settings shows. The database is read here,
// once a while and after each action, not on every frame.
func (a *app) refreshClients() {
	if a.gate == nil {
		a.clients = nil
		return
	}
	a.clients = map[string][]Client{}
	for _, state := range []string{Probation, Rejected, Approved, Blocked} {
		a.clients[state] = a.gate.Clients(state)
	}
}

// clientsView lists the nodes this host's server has seen: the ones waiting
// for approval, the ones approved, and the ones blocked.
func (a *app) clientsView(c *ui.Context) {
	if a.gate == nil {
		return
	}
	if a.clients == nil {
		a.refreshClients()
	}
	theme := c.Theme()
	pending := append(append([]Client{}, a.clients[Probation]...), a.clients[Rejected]...)

	section := func(title string, rows []Client, detail func(Client) string, approve, block bool) {
		ui.Text(c, title).FontSize(18).Bold()
		if len(rows) == 0 {
			ui.Text(c, "None").TextColor(theme.TextMuted)
			return
		}
		for _, cl := range rows {
			key := cl.Key.String()
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, key).SingleLine().MinWidth(0).Grow(1).Tooltip(key)
				ui.Text(c, detail(cl)).TextColor(theme.TextMuted).SingleLine()
				if approve && ui.Button(c, "Approve").Label("Approve "+key).Clicked() {
					if err := a.gate.Approve(cl.Key); err != nil {
						a.status = "Approve failed: " + err.Error()
					} else {
						a.status = "Approved " + cl.Key.ShortString()
					}
					a.refreshClients()
				}
				if block && ui.Button(c, "Block").Label("Block "+key).Clicked() {
					a.gate.Block(cl.Key)
					a.status = "Blocked " + cl.Key.ShortString()
					a.refreshClients()
				}
			})
		}
	}
	section("Pending approval", pending, func(cl Client) string {
		if cl.Name != "" {
			return cl.Name + ": " + cl.Reason
		}
		return cl.Reason
	}, true, true)
	section("Approved", a.clients[Approved], func(cl Client) string {
		by := cl.Name
		if cl.CA == a.gate.selfFP {
			by = "this machine"
		}
		return fmt.Sprintf("%s, until %s", by, cl.Expires.Format(time.DateTime))
	}, false, true)
	section("Blocked", a.clients[Blocked], func(Client) string { return "" }, true, false)
}
