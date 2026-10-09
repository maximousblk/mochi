// Package rustdesk is the RustDesk launcher. It forwards a host's RustDesk
// direct-IP port (TCP 21118, enabled on the host under Settings → Security →
// "Enable direct IP access") to a free loopback port, then starts RustDesk with
// `RustDesk.exe --connect 127.0.0.1:<port>`. The address is also copied to the
// clipboard. Enabling checks that the host accepts the connection first. If
// RustDesk is not installed in Program Files, Enable fails and nothing is left
// running. Disabling closes the forward. Nothing is written to disk.
package rustdesk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/share"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
)

const (
	// DirectPort is the TCP port RustDesk listens on for direct-IP connections.
	DirectPort  = 21118
	dialTimeout = 15 * time.Second
)

type forward struct {
	listener net.Listener
	client   *admission.Dialer
	once     sync.Once
}

// Connector forwards each enabled host's RustDesk direct-IP port and starts
// the RustDesk client on it.
type Connector struct {
	icon     *ui.SVG
	dialing  admission.Dialing
	forwards map[string]*forward
	connector.ServerSwitch
}

// New returns the RustDesk launcher. icon is its tile icon.
func New(icon *ui.SVG, dialing admission.Dialing) *Connector {
	return &Connector{
		icon:     icon,
		dialing:  dialing,
		forwards: map[string]*forward{},
	}
}

func (c *Connector) Name() string                 { return "RustDesk" }
func (c *Connector) Icon() *ui.SVG                { return c.icon }
func (c *Connector) SetHosts([]state.Host)        {}
func (c *Connector) Enabled(host state.Host) bool { _, ok := c.forwards[host.Address]; return ok }

// ServerServe forwards the host's direct-IP port to RustDesk on this machine,
// which must have direct IP access enabled in its own settings.
func (c *Connector) ServerServe(sv *connector.ServerServices) {
	sv.Serve[DirectPort] = forwardTo(fmt.Sprintf("127.0.0.1:%d", DirectPort))
}

// URL returns the loopback address the client connects to while enabled, or "".
func (c *Connector) URL(host state.Host) string {
	if f, ok := c.forwards[host.Address]; ok {
		return f.listener.Addr().String()
	}
	return ""
}

// Enable checks that the host accepts direct-IP connections, starts the
// forward, and launches RustDesk on it. If RustDesk cannot be launched, the
// forward is closed again and the error says so.
func (c *Connector) Enable(host state.Host) error {
	if c.Enabled(host) {
		return nil
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	info, err := tailcat.ParseAddr(tailcat.Addr(host.Address))
	if err != nil {
		return fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := c.dialing.Dialer(info.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	conn, err := client.DialTCPPort(ctx, DirectPort)
	cancel()
	if err != nil {
		client.Close()
		return fmt.Errorf("host does not accept RustDesk direct IP on port %d (enable direct IP access there, and make sure this client is allowed): %w", DirectPort, err)
	}
	conn.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return err
	}
	f := &forward{listener: ln, client: client}
	c.forwards[host.Address] = f
	go f.serve()

	if err := share.StartProcess(exe, "--connect", ln.Addr().String()); err != nil {
		delete(c.forwards, host.Address)
		f.close()
		return fmt.Errorf("start RustDesk: %w", err)
	}
	return nil
}

// Disable stops the forward for host. It is idempotent.
func (c *Connector) Disable(host state.Host) error {
	f, ok := c.forwards[host.Address]
	if !ok {
		return nil
	}
	delete(c.forwards, host.Address)
	f.close()
	return nil
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
	return "", errors.New("RustDesk is not installed in Program Files; install it, or enter the copied address in RustDesk")
}

func (f *forward) close() {
	f.once.Do(func() {
		f.listener.Close()
		f.client.Close()
	})
}

func (f *forward) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *forward) handle(local net.Conn) {
	defer local.Close()
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	remote, err := f.client.DialTCPPort(ctx, DirectPort)
	cancel()
	if err != nil {
		log.Printf("rustdesk: dial host port %d: %v", DirectPort, err)
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
		local, err := net.DialTimeout("tcp", addr, dialTimeout)
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
