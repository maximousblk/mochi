package app

import (
	"fmt"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/egoist/mygo/ui"
)

// clientStates are the admission states the self page lists, in display order.
var clientStates = []string{admission.Probation, admission.Rejected, admission.Approved, admission.Blocked}

// reloadClients reads the lists the self page shows, off the UI thread, and
// replaces a.clients when the read finishes. A request made during a read, such
// as the one after Block, is not dropped: another read follows it, so the lists
// always catch up with the last change.
func (a *app) reloadClients() {
	gate := a.gate
	if gate == nil {
		a.clients = nil
		return
	}
	if !a.clientsReload.start() {
		return
	}
	go func() {
		lists := map[string][]admission.Client{}
		for _, st := range clientStates {
			lists[st] = gate.Clients(st)
		}
		a.update(func() {
			if a.gate == gate {
				a.clients = lists
			}
			if a.clientsReload.done() {
				a.reloadClients()
			}
		})
	}()
}

// clientsView lists the nodes this host's server has seen: the ones waiting for
// approval, the ones approved, and the ones blocked. Approve and Block run off
// the UI thread, and the row's buttons are disabled while its action runs.
func (a *app) clientsView(c *ui.Context) {
	gate := a.gate
	if gate == nil {
		return
	}
	if a.clients == nil {
		a.reloadClients()
		ui.Text(c, "Loading…").TextColor(c.Theme().TextMuted)
		return
	}
	theme := c.Theme()
	pending := append(append([]admission.Client{}, a.clients[admission.Probation]...), a.clients[admission.Rejected]...)

	section := func(title string, rows []admission.Client, detail func(admission.Client) string, approve, block bool) {
		ui.Text(c, title).FontSize(18).Bold()
		if len(rows) == 0 {
			ui.Text(c, "None").TextColor(theme.TextMuted)
			return
		}
		for _, cl := range rows {
			key := cl.Key.String()
			op := "gate|" + key
			busy := a.busy[op]
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, key).SingleLine().MinWidth(0).Grow(1).Tooltip(key)
				ui.Text(c, detail(cl)).TextColor(theme.TextMuted).SingleLine()
				if approve && ui.Button(c, "Approve").Label("Approve "+key).Disabled(busy).Clicked() {
					a.async(op, func() error { return gate.Approve(cl.Key) }, func(err error) {
						if err != nil {
							a.say(page{}, "Approve failed: "+err.Error())
						} else {
							a.say(page{}, "Approved "+cl.Key.ShortString())
						}
						a.reloadClients()
					})
				}
				if block && ui.Button(c, "Block").Label("Block "+key).Disabled(busy).Clicked() {
					a.async(op, func() error { gate.Block(cl.Key); return nil }, func(error) {
						a.say(page{}, "Blocked "+cl.Key.ShortString())
						a.reloadClients()
					})
				}
			})
		}
	}
	section("Pending approval", pending, func(cl admission.Client) string {
		if cl.Name != "" {
			return cl.Name + ": " + cl.Reason
		}
		return cl.Reason
	}, true, true)
	section("Approved", a.clients[admission.Approved], func(cl admission.Client) string {
		by := cl.Name
		if cl.CA == gate.SelfFingerprint() {
			by = "this machine"
		}
		return fmt.Sprintf("%s, until %s", by, cl.Expires.Format(time.DateTime))
	}, false, true)
	section("Blocked", a.clients[admission.Blocked], func(admission.Client) string { return "" }, true, false)
}
