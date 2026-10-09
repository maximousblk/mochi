package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

// The Send text launcher is a one-shot action rather than a toggled
// connector, so it has no enabled state: it sends the typed text to a TCP
// port on the host, as netcat sends stdin, then closes the connection.

//go:embed resources/send.svg
var sendIconData []byte

var textSenderIcon = ui.MustParseSVG(sendIconData)

const sendTimeout = 30 * time.Second

// sendLinger is how long sendText waits after half-closing for the receiver
// to close its side.
const sendLinger = 3 * time.Second

// defaultSendPort is the port tailcat's CLI and browser demo use when none
// is given: their listeners accept any port and hand the stream to the app.
const defaultSendPort = 1

// parsePort returns the port typed in the popup, or defaultSendPort if it is empty.
func parsePort(s string) (uint16, error) {
	if s == "" {
		return defaultSendPort, nil
	}
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid TCP port %q", s)
	}
	return uint16(port), nil
}

// sendText writes text to port on the Tailcat host at address and closes
// the connection. It returns the number of bytes written.
func sendText(ctx context.Context, dialing Dialing, address string, port uint16, text string) (int, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return 0, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := dialing.Dialer(info.Addr())
	defer client.Close()
	conn, err := client.DialTCPPort(ctx, port)
	if err != nil {
		return 0, fmt.Errorf("connect to port %d: %w", port, err)
	}
	defer conn.Close()
	n, err := conn.Write([]byte(text))
	if err != nil {
		return n, err
	}
	// Half-close so the receiver sees end of input, as netcat does on EOF.
	halfCloser, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		return n, errors.New("tailcat connection cannot half-close")
	}
	if err := halfCloser.CloseWrite(); err != nil {
		return n, err
	}
	// Closing the tunnel right away can drop the FIN, so linger until the
	// receiver closes its side or the linger expires. Either way the data was
	// already delivered.
	conn.SetReadDeadline(time.Now().Add(sendLinger))
	io.Copy(io.Discard, conn)
	return n, nil
}

// sendDialog shows the Send text popup for host while a.sendOpen is set.
func (a *app) sendDialog(c *ui.Context, host Host) {
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
func (a *app) submitText(host Host) {
	port, err := parsePort(a.sendPort)
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
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		n, err := sendText(ctx, a.dialing, host.Address, port, body)
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
