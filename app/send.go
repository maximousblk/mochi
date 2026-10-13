package app

import (
	"context"
	"fmt"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/connector/textsender"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// The Send text launcher is a one-shot action, so it has no enabled state: its
// window sends the typed text to a TCP port on the host, then closes.

// sendName is the launcher name the Send text settings are saved under.
const sendName = "Send text"

// sendDialog is the state of one open Send text window. port and body are the
// draft; err is the last failed send, which keeps the window open.
type sendDialog struct {
	host state.Host
	port string
	body string
	err  string
}

func sendKey(address string) string { return "send|" + address }

// openSend opens the Send text window for host. Its port is pre-filled from the
// host's Send text settings, which are read off the UI thread.
func (a *app) openSend(host state.Host) {
	key := sendKey(host.Address)
	if d, ok := a.dialogs[key]; ok {
		d.win.Focus()
		return
	}
	var cfg map[string]string
	a.async(key, func() (err error) {
		cfg, err = a.loadConfig(connector.ClientConfigKey(sendName, host.Address), textsender.ClientFields())
		return err
	}, func(err error) {
		if err != nil {
			a.say(hostScope(host.Address), sendName+" settings not loaded, using defaults: "+err.Error())
		}
		d := &sendDialog{host: host, port: cfg["port"]}
		a.openDialog(key, host.Address, "Send text to "+host.Name, 380, func(c *ui.Context) {
			a.sendView(c, d)
		})
	})
}

// sendView draws the Send text window. Send is disabled while the send runs.
func (a *app) sendView(c *ui.Context, d *sendDialog) {
	theme := c.Theme()
	key := sendKey(d.host.Address)
	busy := a.busy[key]
	ui.TextInput(c, &d.port).Placeholder("Port (default 1)").Label("Port")
	ui.TextArea(c, &d.body).Placeholder("Text to send").Label("Text to send").Height(160)
	if d.err != "" {
		ui.Text(c, d.err).TextColor(theme.Danger)
	}
	ui.Row(c).Gap(8).Children(func() {
		send := "Send"
		if busy {
			send = "Sending…"
		}
		if ui.PrimaryButton(c, send).Disabled(busy).Clicked() {
			a.submitText(d)
		}
		if ui.Button(c, "Cancel").Clicked() {
			a.closeDialog(key)
		}
	})
}

// submitText validates the window's fields and starts the send off the UI
// thread. A send that succeeds closes the window, and one that fails keeps it
// open with the error.
func (a *app) submitText(d *sendDialog) {
	port, err := textsender.ParsePort(d.port)
	if err != nil {
		d.err = err.Error()
		return
	}
	if d.body == "" {
		d.err = "Enter text to send"
		return
	}
	d.err = ""
	body := d.body
	host := d.host
	key := sendKey(host.Address)
	var n int
	a.async(key, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), textsender.Timeout)
		defer cancel()
		var err error
		n, err = textsender.Send(ctx, a.dialing, host.Address, port, body)
		return err
	}, func(err error) {
		if err != nil {
			d.err = "Send failed: " + err.Error()
			return
		}
		a.say(hostScope(host.Address), fmt.Sprintf("Sent %d bytes to %s port %d", n, host.Name, port))
		a.closeDialog(key)
	})
}
