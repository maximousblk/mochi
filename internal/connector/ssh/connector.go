// Package ssh is the SSH launcher. It writes a marked block to the user's SSH
// config so that `ssh <host>` reaches a Mochi host through Tailcat, and it
// implements the `connector-ssh-proxy` command that block runs.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

// Connector writes and removes the managed SSH config blocks of its hosts.
type Connector struct {
	configPath string
	icon       *ui.SVG
	enabled    map[string]bool
	hosts      []state.Host
	connector.ServerSwitch
}

// New returns the SSH launcher managing configPath. icon is its tile icon.
func New(configPath string, icon *ui.SVG) *Connector {
	c := &Connector{
		configPath: configPath,
		icon:       icon,
		enabled:    make(map[string]bool),
	}
	c.Listen()
	return c
}

func (c *Connector) Name() string                 { return "SSH" }
func (c *Connector) Icon() *ui.SVG                { return c.icon }
func (c *Connector) SetHosts(hosts []state.Host)  { c.hosts = append(c.hosts[:0], hosts...) }
func (c *Connector) Enabled(host state.Host) bool { return c.enabled[host.Address] }

// ServerServe serves SSH shell sessions on this machine.
func (c *Connector) ServerServe(sv *connector.ServerServices) { sv.Shell = true }

// Proxy implements `connector-ssh-proxy <tailcat-address> <port>`: it relays
// stdin and stdout to the host's Tailcat TCP port. hosts supplies the stored
// spelling of the address, because Windows OpenSSH lowercases %h.
func Proxy(ctx context.Context, hosts []state.Host, args []string, stdin io.Reader, stdout io.Writer, dialing admission.Dialing) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: mochi connector-ssh-proxy <tailcat-address> <port>")
	}
	addressText := canonicalAddress(hosts, args[0])
	address, err := tailcat.ParseAddr(tailcat.Addr(addressText))
	if err != nil {
		return fmt.Errorf("invalid Tailcat address %q: %w", addressText, err)
	}
	port, err := strconv.ParseUint(args[1], 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("invalid TCP port %q", args[1])
	}
	client := dialing.Dialer(address.Addr())
	defer client.Close()
	conn, err := client.DialTCPPort(ctx, uint16(port))
	if err != nil {
		return fmt.Errorf("connect to Tailcat host port %d: %w", port, err)
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	return proxyStreams(conn, stdin, stdout)
}

func canonicalAddress(hosts []state.Host, address string) string {
	for _, host := range hosts {
		if strings.EqualFold(host.Address, address) {
			return host.Address
		}
	}
	return address
}

func proxyStreams(conn net.Conn, stdin io.Reader, stdout io.Writer) error {
	inputDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdin)
		if err != nil {
			_ = conn.Close()
		} else if halfCloser, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = halfCloser.CloseWrite()
		} else {
			_ = conn.Close()
		}
		inputDone <- err
	}()
	_, outputErr := io.Copy(stdout, conn)
	_ = conn.Close()
	if outputErr != nil {
		return outputErr
	}
	select {
	case inputErr := <-inputDone:
		return inputErr
	default:
		return nil
	}
}

// Refresh reconciles the managed blocks in the SSH config with hosts: blocks
// that exist are rewritten for the running executable, and missing ones mark
// the host as not enabled.
func (c *Connector) Refresh(hosts []state.Host) error {
	data, err := os.ReadFile(c.configPath)
	if errors.Is(err, os.ErrNotExist) {
		for _, host := range hosts {
			c.enabled[host.Address] = false
		}
		return nil
	}
	if err != nil {
		return err
	}
	executable, err := currentExecutablePath()
	if err != nil {
		return err
	}
	text := string(data)
	for _, host := range hosts {
		begin, end := managedBlockMarkers(host)
		if strings.Contains(text, begin) {
			c.enabled[host.Address] = true
			text = removeManagedBlock(text, begin, end)
			text = appendManagedBlock(text, managedBlock(host, begin, end, executable))
		} else {
			c.enabled[host.Address] = false
		}
	}
	if text != string(data) {
		return c.write([]byte(text))
	}
	return nil
}

func (c *Connector) Connect(host state.Host) error {
	return c.set(host, true)
}

func (c *Connector) Disconnect(host state.Host) error {
	return c.set(host, false)
}

func (c *Connector) set(host state.Host, enabled bool) error {
	data, err := os.ReadFile(c.configPath)
	if errors.Is(err, os.ErrNotExist) {
		if !enabled {
			c.enabled[host.Address] = false
			return nil
		}
	} else if err != nil {
		return err
	}
	begin, end := managedBlockMarkers(host)
	if !enabled && !strings.Contains(string(data), begin) {
		c.enabled[host.Address] = false
		return nil
	}
	text := removeManagedBlock(string(data), begin, end)
	if enabled {
		executable, err := currentExecutablePath()
		if err != nil {
			return err
		}
		text = appendManagedBlock(text, managedBlock(host, begin, end, executable))
	}
	if err := c.write([]byte(text)); err != nil {
		return err
	}
	c.enabled[host.Address] = enabled
	return nil
}

func (c *Connector) write(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(c.configPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(c.configPath, data, 0600)
}

func appendManagedBlock(text, block string) string {
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + block
}
