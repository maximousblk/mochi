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

// Connector represents a host-specific integration managed by Mochi.
// Disable must be idempotent so cleanup can safely be retried.
//
// Each connector also has a server side: the service this machine offers
// other machines while the connector is on. The server reads it per
// connection, so a change takes effect without restarting the server.
type Connector interface {
	Name() string
	Icon() *ui.SVG
	SetHosts([]state.Host)
	Enabled(state.Host) bool
	Connect(state.Host) error
	Disconnect(state.Host) error

	// ServerEnabled reports whether this machine offers the connector's service.
	ServerEnabled() bool
	// Listen turns the service on, doing any setup it needs. If the setup
	// fails the service stays off and the error says why.
	Listen() error
	// StopListening turns the service off.
	StopListening() error
	// Healthy reports whether the service is on and still able to serve. It
	// is false while the service is off.
	Healthy() bool
	// ServerServe adds the service to sv. Called only while ServerEnabled is true.
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
