// Package ssh is the SSH launcher. Its server side runs an SSH server in process,
// on this machine's Tailcat port ServePort. Its client side tunnels to a host's
// SSH server, and copies the `ssh` command that reaches it through the tunnel.
package ssh

import (
	"fmt"
	"log"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/sshserver"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// ServePort is the Tailcat port the SSH server listens on. It is not 22, so the
// machine's own sshd, if it has one, is left alone.
const ServePort uint16 = 22022

// Connector serves SSH while listening, and tunnels to a host's SSH while
// connected.
type Connector struct {
	ports    *connector.Ports
	seed     []byte
	copyText func(string)
	icon     *ui.SVG
	dialing  admission.Dialing
	access   *connector.DiskAccess
}

// New returns the SSH launcher. seed is this machine's identity seed, from
// which its SSH host key is derived. copyText puts text on the clipboard, and
// icon is its tile icon.
func New(ports *connector.Ports, seed []byte, copyText func(string), icon *ui.SVG, dialing admission.Dialing) *Connector {
	return &Connector{ports: ports, seed: seed, copyText: copyText, icon: icon, dialing: dialing, access: connector.SystemDiskAccess}
}

func (c *Connector) Name() string  { return "SSH" }
func (c *Connector) Icon() *ui.SVG { return c.icon }

// Listen serves the SSH server on ServePort.
func (c *Connector) Listen() error {
	hostKey, err := sshserver.HostKey(c.seed)
	if err != nil {
		return fmt.Errorf("SSH host key: %w", err)
	}
	c.ports.Register(ServePort, sshserver.New(hostKey, log.Printf).Handle)
	return nil
}

// Stop stops serving SSH on ServePort.
func (c *Connector) Stop() error {
	c.ports.Unregister(ServePort)
	return nil
}

// Healthy reports whether this machine serves SSH, and its shells can reach
// everything the user can, as a terminal's do. Shells are Mochi's children, so
// on macOS they need Mochi to have Full Disk Access (see connector.DiskAccess).
func (c *Connector) Healthy() bool {
	return c.ports.Registered(ServePort) && c.access.Check()
}

// Connect tunnels to the host's SSH server, and copies the command that reaches
// it through the tunnel. The host key is matched by the host's node key, so
// ssh's known hosts do not depend on the loopback address. The user adds their
// own user name, which selects the account on the host.
func (c *Connector) Connect(host state.Host) (connector.Client, error) {
	info, err := connector.ParseHost(host.Address)
	if err != nil {
		return nil, err
	}
	t, err := connector.OpenTunnel(c.dialing.Dialer(info.Addr()), ServePort)
	if err != nil {
		return nil, err
	}
	c.copyText(fmt.Sprintf("ssh -p %d -o HostKeyAlias=mochi-node-%s 127.0.0.1", t.Port(), info.ServerPublic.NodePublic.UntypedHexString()))
	return t, nil
}
