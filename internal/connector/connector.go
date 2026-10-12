// Package connector defines what every launcher implements: the client side,
// which reaches a host, and the server side, which this machine offers while
// the launcher is on.
package connector

import (
	"net"
	"strings"
	"sync/atomic"

	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

// Connector is a launcher. Its client side reaches one remote host at a time;
// its server side is what this machine offers to other machines.
//
// The server reads the server side per connection, so Listen and StopListening
// take effect without restarting the server.
type Connector interface {
	// Name is the launcher's label, such as "SSH". Its lowercase form is part
	// of the settings key that stores the server-side switch (SettingKey).
	Name() string
	// Icon is drawn on the launcher's tile.
	Icon() *ui.SVG
	// SetHosts gives the connector the current host list. Called at startup and
	// whenever hosts change, for connectors that keep per-host state.
	SetHosts([]state.Host)

	// Enabled reports whether the client side is connected to host.
	Enabled(state.Host) bool
	// Connect starts the client side for host. If it fails, nothing is left
	// running. Connecting an already connected host does nothing.
	Connect(state.Host) error
	// Disconnect stops the client side for host. It must be idempotent, so a
	// failed cleanup can be retried, and a host that is not connected is fine.
	Disconnect(state.Host) error

	// ServerEnabled reports whether the switch for this machine's service is
	// on. It says nothing about whether the service works; see Healthy.
	ServerEnabled() bool
	// Listen turns the switch on. If it fails, the switch stays off and the
	// error says why. It is called when the user turns the service on, and at
	// startup for each service that was left on.
	Listen() error
	// StopListening turns the switch off.
	StopListening() error
	// Healthy reports whether the service is on and able to serve right now.
	// It is false while the switch is off. A connector that depends on
	// something outside Mochi checks it here, and the UI keeps the connector
	// on but draws it yellow while it is false. It may dial, so it is called
	// off the UI thread.
	Healthy() bool
	// ServerServe adds this connector's service to sv. Called only while
	// ServerEnabled is true.
	ServerServe(sv *ServerServices)
}

// ServerServices is what the enabled connectors offer on this machine's Tailcat
// server.
type ServerServices struct {
	// Shell and Files configure the SSH server on port 22, which serves SSH
	// sessions and SFTP. Port 22 is served only if one of them is set.
	Shell bool
	Files *tailcat.FileService
	// Serve maps a Tailcat port to the handler for its connections. A handler
	// gets each connection to its port, and may serve it however it likes.
	Serve map[uint16]func(net.Conn)
}

// ServerSwitch is a connector's server-side state. Connection handlers read it
// while the UI writes it, so it is atomic.
type ServerSwitch struct{ on atomic.Bool }

func (s *ServerSwitch) ServerEnabled() bool { return s.on.Load() }

// Listen turns the service on. A connector with setup overrides it.
func (s *ServerSwitch) Listen() error { s.on.Store(true); return nil }

// StopListening turns the service off.
func (s *ServerSwitch) StopListening() error { s.on.Store(false); return nil }

// Healthy is true while the service is on. A connector that depends on
// something outside Mochi overrides it.
func (s *ServerSwitch) Healthy() bool { return s.on.Load() }

// SettingKey is the settings key a connector's server-side switch is saved under.
func SettingKey(c Connector) string {
	return "connector_server:" + strings.ToLower(c.Name())
}
