package app

import (
	"context"
	"fmt"

	"github.com/ashwin-y/mochi/internal/connector/textsender"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// The Send text launcher is a one-shot action, so it has no enabled state: the
// popup sends the typed text to a TCP port on the host, then closes.

// sendDialog shows the Send text popup for host while a.sendOpen is set.
func (a *app) sendDialog(c *ui.Context, host state.Host) {
	ui.Modal(c, &a.sendOpen, func() {
		ui.Text(c, "Send text to "+host.Name).FontSize(18).Bold()
		ui.TextInput(c, &a.sendPort).Placeholder("Port (default 1)").Label("Port")
		ui.TextArea(c, &a.sendBody).Placeholder("Text to send").Label("Text to send").Height(160)
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "Send").Disabled(a.sendBusy).Clicked() {
				a.submitText(host)
			}
			if ui.Button(c, "Cancel").Clicked() {
				a.sendOpen = false
			}
		})
	})
}

// submitText validates the popup fields and starts the send.
func (a *app) submitText(host state.Host) {
	port, err := textsender.ParsePort(a.sendPort)
	if err != nil {
		a.status = err.Error()
		return
	}
	if a.sendBody == "" {
		a.status = "Enter text to send"
		return
	}
	a.sendBusy = true
	a.status = "Sending text…"
	body := a.sendBody
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), textsender.Timeout)
		defer cancel()
		n, err := textsender.Send(ctx, a.dialing, host.Address, port, body)
		a.update(func() {
			a.sendBusy = false
			if err != nil {
				a.status = "Send failed: " + err.Error()
				return
			}
			a.sendOpen = false
			a.sendBody = ""
			a.status = fmt.Sprintf("Sent %d bytes to %s port %d", n, host.Name, port)
		})
	}()
}
