package main

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"

	"github.com/tailscale/tailcat"

	"github.com/egoist/mygo/ui"
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
	SetHosts([]Host)
	Enabled(Host) bool
	Enable(Host) error
	Disable(Host) error
	CLICommand() string
	RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error

	// ServerEnabled reports whether this machine offers the connector's service.
	ServerEnabled() bool
	SetServerEnabled(on bool)
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

// serverSwitch is a connector's server-side state. Connection handlers read it
// while the UI writes it, so it is atomic.
type serverSwitch struct{ on atomic.Bool }

func (s *serverSwitch) ServerEnabled() bool      { return s.on.Load() }
func (s *serverSwitch) SetServerEnabled(on bool) { s.on.Store(on) }

// serverSettingKey is the settings key a connector's server-side switch is saved under.
func serverSettingKey(c Connector) string {
	return "connector_server:" + strings.ToLower(c.Name())
}

func newConnectors(sshConfigPath string, dialing Dialing) []Connector {
	return []Connector{
		NewSSHConfigConnector(sshConfigPath, sshIcon),
		NewFilesConnector(folderIcon, dialing),
		NewWebDAVConnector(webdavIcon, dialing),
		NewRustDeskConnector(rustdeskIcon, dialing),
	}
}
