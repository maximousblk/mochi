package main

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

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

type SSHConfigConnector struct {
	configPath string
	icon       *ui.SVG
	enabled    map[string]bool
	hosts      []Host
	serverSwitch
}

func NewSSHConfigConnector(configPath string, icon *ui.SVG) *SSHConfigConnector {
	c := &SSHConfigConnector{
		configPath: configPath,
		icon:       icon,
		enabled:    make(map[string]bool),
	}
	c.SetServerEnabled(true)
	return c
}

func (c *SSHConfigConnector) Name() string           { return "SSH" }
func (c *SSHConfigConnector) Icon() *ui.SVG          { return c.icon }
func (c *SSHConfigConnector) SetHosts(hosts []Host)  { c.hosts = append(c.hosts[:0], hosts...) }
func (c *SSHConfigConnector) Enabled(host Host) bool { return c.enabled[host.Address] }
func (c *SSHConfigConnector) CLICommand() string     { return "connector-ssh-proxy" }

// ServerServe serves SSH shell sessions on this machine.
func (c *SSHConfigConnector) ServerServe(sv *ServerServices) { sv.Shell = true }

func (c *SSHConfigConnector) RunCommand(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, dialing Dialing) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: mochi connector-ssh-proxy <tailcat-address> <port>")
	}
	addressText := c.canonicalAddress(args[0])
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

func (c *SSHConfigConnector) canonicalAddress(address string) string {
	for _, host := range c.hosts {
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

func (c *SSHConfigConnector) Refresh(hosts []Host) error {
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

func (c *SSHConfigConnector) Enable(host Host) error {
	return c.set(host, true)
}

func (c *SSHConfigConnector) Disable(host Host) error {
	return c.set(host, false)
}

func (c *SSHConfigConnector) set(host Host, enabled bool) error {
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

func (c *SSHConfigConnector) write(data []byte) error {
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
