// Package textsender sends typed text to a TCP port on a Tailcat host, the way
// netcat sends stdin. Sending is a one-shot action. Receiving is the server side
// of the same port: Receiver accepts raw text on DefaultPort and hands it to the
// app, so stock tailcat's `echo hello | tailcat <address>` lands there.
package textsender

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/maximousblk/mochi/internal/admission"
	"github.com/tailscale/tailcat"
)

// Timeout bounds one send, from dialing to the receiver closing its side.
const Timeout = 30 * time.Second

// linger is how long Send waits after half-closing for the receiver to close
// its side.
const linger = 3 * time.Second

// DefaultPort is the port tailcat's CLI and browser demo use when none is
// given: their listeners accept any port and hand the stream to the app.
const DefaultPort = 1

// ParsePort returns the port typed in the window, or DefaultPort if it is empty.
func ParsePort(s string) (uint16, error) {
	if s == "" {
		return DefaultPort, nil
	}
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid TCP port %q", s)
	}
	return uint16(port), nil
}

// Send writes text to port on the Tailcat host at address and closes the
// connection. It returns the number of bytes written.
func Send(ctx context.Context, dialing admission.Dialing, address string, port uint16, text string) (int, error) {
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
	conn.SetReadDeadline(time.Now().Add(linger))
	io.Copy(io.Discard, conn)
	return n, nil
}
