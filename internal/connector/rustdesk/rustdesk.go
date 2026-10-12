// Package rustdesk is the RustDesk launcher. Its server side forwards this
// machine's RustDesk direct-IP port (TCP 21118, enabled under Settings →
// Security → "Enable direct IP access") to RustDesk on this machine. Its client
// side tunnels to a host's direct-IP port, and starts RustDesk with
// `RustDesk.exe --connect 127.0.0.1:<port>` on the tunnel. Connecting checks that
// the host accepts the connection first. If RustDesk is not installed in Program
// Files, Connect fails and nothing is left running.
package rustdesk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/share"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

const (
	// DirectPort is the TCP port RustDesk listens on for direct-IP connections.
	DirectPort  = 21118
	dialTimeout = 15 * time.Second
)

// Connector serves this machine's RustDesk direct-IP port while listening, and
// starts the RustDesk client on a host's direct-IP port while connected.
type Connector struct {
	ports   *connector.Ports
	icon    *ui.SVG
	dialing admission.Dialing
}

// New returns the RustDesk launcher. icon is its tile icon.
func New(ports *connector.Ports, icon *ui.SVG, dialing admission.Dialing) *Connector {
	return &Connector{ports: ports, icon: icon, dialing: dialing}
}

func (c *Connector) Name() string  { return "RustDesk" }
func (c *Connector) Icon() *ui.SVG { return c.icon }

// Connect checks that the host accepts direct-IP connections, tunnels to its
// direct-IP port, and launches RustDesk on the tunnel. If RustDesk cannot be
// launched, the tunnel is closed and the error says so.
func (c *Connector) Connect(host state.Host) (connector.Client, error) {
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	info, err := connector.ParseHost(host.Address)
	if err != nil {
		return nil, err
	}
	client := c.dialing.Dialer(info.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	conn, err := client.DialTCPPort(ctx, DirectPort)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("host does not accept RustDesk direct IP on port %d (enable direct IP access there, and make sure this client is allowed): %w", DirectPort, err)
	}
	conn.Close()

	t, err := connector.OpenTunnel(client, DirectPort)
	if err != nil {
		return nil, err
	}
	if err := share.StartProcess(exe, "--connect", fmt.Sprintf("127.0.0.1:%d", t.Port())); err != nil {
		t.Disconnect()
		return nil, fmt.Errorf("start RustDesk: %w", err)
	}
	return t, nil
}

// executable finds RustDesk.exe in the standard Program Files install.
func executable() (string, error) {
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		dir := os.Getenv(env)
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "RustDesk", "RustDesk.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("RustDesk is not installed in Program Files; install it to connect")
}
