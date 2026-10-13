// Package ssh is the SSH launcher. Its server side runs an SSH server in process,
// on this machine's Tailcat port (ServePort by default). Its client side tunnels
// to a host's SSH server, and copies the `ssh` command that reaches it through the
// tunnel.
package ssh

import (
	"fmt"
	"log"
	"sync"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/sshserver"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// ServePort is the default Tailcat port the SSH server listens on. It is not 22,
// so the machine's own sshd, if it has one, is left alone.
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

	// mu guards port, which Healthy reads from off the UI thread.
	mu sync.Mutex
	// port is the Tailcat port Listen claimed, or 0 while the server is off.
	port uint16
}

// New returns the SSH launcher. seed is this machine's identity seed, from
// which its SSH host key is derived. copyText puts text on the clipboard, and
// icon is its tile icon.
func New(ports *connector.Ports, seed []byte, copyText func(string), icon *ui.SVG, dialing admission.Dialing) *Connector {
	return &Connector{ports: ports, seed: seed, copyText: copyText, icon: icon, dialing: dialing, access: connector.SystemDiskAccess}
}

func (c *Connector) Name() string  { return "SSH" }
func (c *Connector) Icon() *ui.SVG { return c.icon }

// ServerFields are the settings of the SSH server on this machine.
func (c *Connector) ServerFields() []connector.Field {
	return []connector.Field{{
		Key:     "port",
		Label:   "Tailcat port",
		Default: fmt.Sprint(ServePort),
		Help:    "The Tailcat port this machine serves SSH on.",
		Check:   connector.CheckPort,
	}}
}

// ClientFields are the settings of the tunnel to one host.
func (c *Connector) ClientFields() []connector.Field {
	return []connector.Field{
		{
			Key:     "port",
			Label:   "Host's SSH port",
			Default: fmt.Sprint(ServePort),
			Help:    "The port the host serves SSH on.",
			Check:   connector.CheckPort,
		},
		{
			Key:     "user",
			Label:   "User name",
			Default: "",
			Help:    "The account on the host; empty leaves it to ssh.",
		},
	}
}

// Listen serves the SSH server on the Tailcat port in cfg.
func (c *Connector) Listen(cfg connector.Config) error {
	port, err := cfg.Port("port")
	if err != nil {
		return err
	}
	hostKey, err := sshserver.HostKey(c.seed)
	if err != nil {
		return fmt.Errorf("SSH host key: %w", err)
	}
	if err := c.ports.Claim(port, sshserver.New(hostKey, log.Printf).Handle); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.port = port
	return nil
}

// Stop stops serving SSH on the port Listen claimed.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port != 0 {
		c.ports.Unregister(c.port)
		c.port = 0
	}
	return nil
}

// Healthy reports whether this machine serves SSH, and its shells can reach
// everything the user can, as a terminal's do. Shells are Mochi's children, so
// on macOS they need Mochi to have Full Disk Access (see connector.DiskAccess).
func (c *Connector) Healthy() bool {
	c.mu.Lock()
	port := c.port
	c.mu.Unlock()
	return port != 0 && c.ports.Registered(port) && c.access.Check()
}

// Connect tunnels to the host's SSH server, and copies the command that reaches
// it through the tunnel. The host key is matched by the host's node key, so
// ssh's known hosts do not depend on the loopback address. A user name in cfg is
// put in front of the loopback address, which selects the account on the host.
func (c *Connector) Connect(host state.Host, cfg connector.Config) (connector.Client, error) {
	info, err := connector.ParseHost(host.Address)
	if err != nil {
		return nil, err
	}
	port, err := cfg.Port("port")
	if err != nil {
		return nil, err
	}
	t, err := connector.OpenTunnel(c.dialing.Dialer(info.Addr()), port)
	if err != nil {
		return nil, err
	}
	target := "127.0.0.1"
	if user := cfg["user"]; user != "" {
		target = user + "@" + target
	}
	c.copyText(fmt.Sprintf("ssh -p %d -o HostKeyAlias=mochi-node-%s %s", t.Port(), info.ServerPublic.NodePublic.UntypedHexString(), target))
	return t, nil
}
