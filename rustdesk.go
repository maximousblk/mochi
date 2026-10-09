package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

// The RustDesk launcher forwards a host's RustDesk direct-IP port (TCP 21118,
// enabled on the host under Settings → Security → "Enable direct IP access")
// to a free loopback port, then starts RustDesk with
// `RustDesk.exe --connect 127.0.0.1:<port>`. The address is also copied to the
// clipboard. Enabling checks that the host accepts the connection first. If
// RustDesk is not installed in Program Files, Enable fails and nothing is left
// running. Disabling closes the forward. Nothing is written to disk.

//go:embed resources/rustdesk.svg
var rustdeskIconData []byte

var rustdeskIcon = ui.MustParseSVG(rustdeskIconData)

const (
	rustdeskDirectPort  = 21118
	rustdeskDialTimeout = 15 * time.Second
)

type rustdeskForward struct {
	listener net.Listener
	client   *Dialer
	once     sync.Once
}

// RustDeskConnector forwards each enabled host's RustDesk direct-IP port and
// starts the RustDesk client on it.
type RustDeskConnector struct {
	icon     *ui.SVG
	dialing  Dialing
	forwards map[string]*rustdeskForward
	serverSwitch
	// launch starts a program without waiting for it. Replaced in tests.
	launch func(exe string, args ...string) error
}

func NewRustDeskConnector(icon *ui.SVG, dialing Dialing) *RustDeskConnector {
	return &RustDeskConnector{
		icon:     icon,
		dialing:  dialing,
		forwards: map[string]*rustdeskForward{},
		launch:   startProcess,
	}
}

func (c *RustDeskConnector) Name() string           { return "RustDesk" }
func (c *RustDeskConnector) Icon() *ui.SVG          { return c.icon }
func (c *RustDeskConnector) SetHosts([]Host)        {}
func (c *RustDeskConnector) CLICommand() string     { return "" }
func (c *RustDeskConnector) Enabled(host Host) bool { _, ok := c.forwards[host.Address]; return ok }

// ServerServe forwards the host's direct-IP port to RustDesk on this machine,
// which must have direct IP access enabled in its own settings.
func (c *RustDeskConnector) ServerServe(sv *ServerServices) {
	sv.Serve[rustdeskDirectPort] = forwardTo(fmt.Sprintf("127.0.0.1:%d", rustdeskDirectPort))
}

func (c *RustDeskConnector) RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error {
	return errors.New("rustdesk has no command-line mode")
}

// URL returns the loopback address the client connects to while enabled, or "".
func (c *RustDeskConnector) URL(host Host) string {
	if f, ok := c.forwards[host.Address]; ok {
		return f.listener.Addr().String()
	}
	return ""
}

// Enable checks that the host accepts direct-IP connections, starts the
// forward, and launches RustDesk on it. If RustDesk cannot be launched, the
// forward is closed again and the error says so.
func (c *RustDeskConnector) Enable(host Host) error {
	if c.Enabled(host) {
		return nil
	}
	exe, err := rustdeskExecutable()
	if err != nil {
		return err
	}
	info, err := tailcat.ParseAddr(tailcat.Addr(host.Address))
	if err != nil {
		return fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := c.dialing.Dialer(info.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), rustdeskDialTimeout)
	conn, err := client.DialTCPPort(ctx, rustdeskDirectPort)
	cancel()
	if err != nil {
		client.Close()
		return fmt.Errorf("host does not accept RustDesk direct IP on port %d (enable direct IP access there, and make sure this client is allowed): %w", rustdeskDirectPort, err)
	}
	conn.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return err
	}
	f := &rustdeskForward{listener: ln, client: client}
	c.forwards[host.Address] = f
	go f.serve()

	if err := c.launch(exe, "--connect", ln.Addr().String()); err != nil {
		delete(c.forwards, host.Address)
		f.close()
		return fmt.Errorf("start RustDesk: %w", err)
	}
	return nil
}

// Disable stops the forward for host. It is idempotent.
func (c *RustDeskConnector) Disable(host Host) error {
	f, ok := c.forwards[host.Address]
	if !ok {
		return nil
	}
	delete(c.forwards, host.Address)
	f.close()
	return nil
}

// rustdeskExecutable finds RustDesk.exe in the standard Program Files install.
func rustdeskExecutable() (string, error) {
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
	return "", errors.New("RustDesk is not installed in Program Files; install it, or enter the copied address in RustDesk")
}

// startProcess starts exe with args and does not wait for it to exit.
func startProcess(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (f *rustdeskForward) close() {
	f.once.Do(func() {
		f.listener.Close()
		f.client.Close()
	})
}

func (f *rustdeskForward) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *rustdeskForward) handle(local net.Conn) {
	defer local.Close()
	ctx, cancel := context.WithTimeout(context.Background(), rustdeskDialTimeout)
	remote, err := f.client.DialTCPPort(ctx, rustdeskDirectPort)
	cancel()
	if err != nil {
		log.Printf("rustdesk: dial host port %d: %v", rustdeskDirectPort, err)
		return
	}
	defer remote.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(remote, local)
		halfClose(remote)
	}()
	go func() {
		defer wg.Done()
		io.Copy(local, remote)
		halfClose(local)
	}()
	wg.Wait()
}

// forwardTo returns a handler that pipes each connection to the local TCP
// address addr, and closes both sides when either one ends.
func forwardTo(addr string) func(net.Conn) {
	return func(remote net.Conn) {
		defer remote.Close()
		local, err := net.DialTimeout("tcp", addr, rustdeskDialTimeout)
		if err != nil {
			return
		}
		defer local.Close()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			io.Copy(local, remote)
			halfClose(local)
		}()
		go func() {
			defer wg.Done()
			io.Copy(remote, local)
			halfClose(remote)
		}()
		wg.Wait()
	}
}

func halfClose(conn net.Conn) {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
