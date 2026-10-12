// Package connector defines what every launcher implements. A launcher's client
// side opens a connection to one host and returns the Client that holds it. Its
// server side is a Tailcat port this machine serves, which the launcher
// registers in Ports while it listens.
package connector

import (
	"fmt"
	"strings"
	"time"

	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

// Connector is a launcher.
type Connector interface {
	// Name is the launcher's label, such as "SSH". Its lowercase form is part
	// of the settings key that stores the server-side switch (SettingKey).
	Name() string
	// Icon is drawn on the launcher's tile.
	Icon() *ui.SVG

	// Connect opens a client to host and does the launcher's hand-off, such as
	// copying a command or opening a folder. If it fails, nothing is left
	// running.
	Connect(host state.Host) (Client, error)

	// Listen turns the server side on. If it fails, the server side stays off
	// and the error says why. It is called when the user turns it on, and at
	// startup for each launcher that was left on.
	Listen() error
	// Stop turns the server side off. Connections already open are not closed.
	Stop() error
	// Healthy reports whether the server side is on and able to serve right
	// now, so it is false while it is off. A launcher that depends on something
	// outside Mochi checks that here, and the UI keeps the launcher on but draws
	// it yellow while Healthy is false. It may dial, so it is called off the UI
	// thread.
	Healthy() bool
}

// Client is a launcher's connection to one host.
type Client interface {
	// LastUsed is the last time data moved through the client. It is time.Now()
	// while a connection is open.
	LastUsed() time.Time
	// Disconnect closes the client. It is idempotent.
	Disconnect() error
}

// SettingKey is the settings key a connector's server-side switch is saved under.
func SettingKey(c Connector) string {
	return "connector_server:" + strings.ToLower(c.Name())
}

// ParseHost decodes a host's Tailcat address.
func ParseHost(address string) (tailcat.ConnInfo, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return tailcat.ConnInfo{}, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	return info, nil
}
