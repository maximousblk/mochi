package textsender

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/maximousblk/mochi/internal/connector"
)

// MaxBytes caps the text one send keeps. A longer send is cut to this many
// bytes and marked Truncated, so a runaway sender cannot fill the database.
const MaxBytes = 64 << 10

// Text is one message received on the Tailcat port.
type Text struct {
	// From is the address of the connection that carried the text. The app
	// maps it to a sender name.
	From net.Addr
	Body string
	// Truncated is set when the sender sent more than MaxBytes.
	Truncated bool
}

// Receiver is the server side of the Send text launcher. It takes raw text on
// the port it is configured to serve, the way netcat writes stdin, and passes
// each message to onText.
type Receiver struct {
	ports     *connector.Ports
	icon      *ui.SVG
	canNotify func() bool
	onText    func(Text)

	mu   sync.Mutex
	port uint16 // the port Listen claimed; 0 while the receiver is off
}

// NewReceiver returns a Receiver that registers on ports. canNotify reports
// whether the app can show notifications, which the receiver needs to be
// healthy because a text nobody is told about is easy to miss. onText runs on
// the connection's goroutine, once per message.
func NewReceiver(ports *connector.Ports, icon *ui.SVG, canNotify func() bool, onText func(Text)) *Receiver {
	return &Receiver{ports: ports, icon: icon, canNotify: canNotify, onText: onText}
}

// Name is the launcher's label. Its settings key is "connector_config:server:send text".
func (r *Receiver) Name() string { return "Send text" }

// Icon is drawn on the launcher's tile.
func (r *Receiver) Icon() *ui.SVG { return r.icon }

// ServerFields are the settings of the text receiver on this machine.
func (r *Receiver) ServerFields() []connector.Field {
	return []connector.Field{{
		Key:     "port",
		Label:   "Tailcat port",
		Default: fmt.Sprint(DefaultPort),
		Help:    "The port other Mochi clients send text to.",
		Check:   connector.CheckPort,
	}}
}

// ClientFields are the settings for sending text to a host. The Send text dialog
// pre-fills its port from them.
func ClientFields() []connector.Field {
	return []connector.Field{{
		Key:     "port",
		Label:   "Default port",
		Default: fmt.Sprint(DefaultPort),
		Help:    "The port the Send text dialog sends to unless you change it.",
		Check:   connector.CheckPort,
	}}
}

// Listen registers the handler on the configured Tailcat port. It fails if
// another launcher already serves that port.
func (r *Receiver) Listen(cfg connector.Config) error {
	port, err := cfg.Port("port")
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ports.Claim(port, r.handle); err != nil {
		return err
	}
	r.port = port
	return nil
}

// Stop unregisters the handler. Texts already being read are still delivered.
func (r *Receiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.port != 0 {
		r.ports.Unregister(r.port)
		r.port = 0
	}
	return nil
}

// Healthy reports whether the port is registered and the app can notify. Without
// notifications a received text would not be shown to anyone, so the tile is
// drawn yellow.
func (r *Receiver) Healthy() bool {
	r.mu.Lock()
	port := r.port
	r.mu.Unlock()
	return port != 0 && r.ports.Registered(port) && r.canNotify()
}

// handle reads one message until the sender closes its side, as netcat does
// on EOF. The deadline covers the whole message, not each read.
func (r *Receiver) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(Timeout))
	// Read one byte past the cap so that a message exactly at the cap is not
	// marked truncated.
	buf, _ := io.ReadAll(io.LimitReader(conn, MaxBytes+1))
	truncated := len(buf) > MaxBytes
	if truncated {
		buf = buf[:MaxBytes]
		// Keep reading and discarding until the sender finishes. Closing now
		// would reset the tunnel while the sender is still writing, and the
		// sender would see an error for text we did keep.
		io.Copy(io.Discard, conn)
	}
	// Cut text can end in the middle of a multi-byte rune, so the cap is
	// applied before the UTF-8 repair.
	body := strings.ToValidUTF8(string(buf), "\uFFFD")
	if body == "" {
		return
	}
	r.onText(Text{From: conn.RemoteAddr(), Body: body, Truncated: truncated})
}
