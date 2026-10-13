// Package webdav is the WebDAV launcher. Its server side serves this machine's
// home volume read-write as WebDAV on the Tailcat port ServePort, with its own
// locks, so Explorer and Finder speak WebDAV end to end. Its client side tunnels
// to a Mochi host's WebDAV port, and opens the share in the platform file
// manager. The tunnel is the access check: a host admits only the machines it
// approves, so the share needs no credential of its own.
//
// Errors, and with --verbose every request, are logged to webdav.log beside the
// state database.
package webdav

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/share"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	xwebdav "golang.org/x/net/webdav"
)

// ServePort is the Tailcat port a Mochi host serves WebDAV on.
const ServePort uint16 = 8080

// Connector serves this machine's home volume while listening, and mounts a
// host's WebDAV share while connected.
type Connector struct {
	ports    *connector.Ports
	copyText func(string)
	icon     *ui.SVG
	dialing  admission.Dialing
	access   *connector.DiskAccess
	logf     func(format string, args ...any)

	// mu guards port, which Healthy reads from off the UI thread.
	mu sync.Mutex
	// port is the Tailcat port Listen claimed, or 0 while the server is off.
	port uint16
}

// New returns the WebDAV launcher. copyText puts text on the clipboard, and icon
// is its tile icon.
func New(ports *connector.Ports, copyText func(string), icon *ui.SVG, dialing admission.Dialing) *Connector {
	logger := &lazyLog{}
	return newConnector(ports, copyText, icon, dialing, logger.Printf)
}

// newConnector returns a launcher that logs to logf.
func newConnector(ports *connector.Ports, copyText func(string), icon *ui.SVG, dialing admission.Dialing, logf func(format string, args ...any)) *Connector {
	return &Connector{ports: ports, copyText: copyText, icon: icon, dialing: dialing, access: connector.SystemDiskAccess, logf: logf}
}

// newHandler serves fs as WebDAV at the root of the URL. webdav's own handler
// keeps locks, ETags and PROPFIND as the filesystem defines them.
func newHandler(fs xwebdav.FileSystem, logf func(format string, args ...any)) http.Handler {
	h := &xwebdav.Handler{
		FileSystem: fs,
		LockSystem: xwebdav.NewMemLS(),
		Logger: func(req *http.Request, err error) {
			if err != nil {
				logf("error %s %s: %v", req.Method, req.URL.Path, err)
			}
		},
	}
	return share.LogRequests(logf, h)
}

// lazyLog opens webdav.log on its first message, so a host that never fails
// writes no file.
type lazyLog struct {
	once sync.Once
	log  *log.Logger
}

func (l *lazyLog) Printf(format string, args ...any) {
	l.once.Do(func() {
		f, err := share.OpenLog()
		if err != nil {
			l.log = log.New(os.Stderr, "", log.LstdFlags)
			return
		}
		l.log = log.New(f, "", log.LstdFlags)
	})
	l.log.Printf(format, args...)
}

// serveHTTPOnConn returns a Tailcat port handler that serves h on the one
// connection it is given, until that connection closes.
func serveHTTPOnConn(h http.Handler) func(net.Conn) {
	return func(conn net.Conn) {
		l := newOneConnListener(conn)
		srv := &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 30 * time.Second,
			IdleTimeout:       2 * time.Minute,
			ConnState: func(_ net.Conn, cs http.ConnState) {
				if cs == http.StateClosed || cs == http.StateHijacked {
					l.release()
				}
			},
		}
		srv.Serve(l)
		conn.Close()
	}
}

// oneConnListener hands http.Server a single connection, then blocks until that
// connection closes.
type oneConnListener struct {
	conn  net.Conn
	taken atomic.Bool
	done  chan struct{}
	once  sync.Once
}

func newOneConnListener(conn net.Conn) *oneConnListener {
	return &oneConnListener{conn: conn, done: make(chan struct{})}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.taken.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error   { l.release(); return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

func (l *oneConnListener) release() { l.once.Do(func() { close(l.done) }) }

func (c *Connector) Name() string  { return "WebDAV" }
func (c *Connector) Icon() *ui.SVG { return c.icon }

// ServerFields are the settings of the WebDAV share on this machine.
func (c *Connector) ServerFields() []connector.Field {
	return []connector.Field{
		{
			Key:     "port",
			Label:   "Tailcat port",
			Default: fmt.Sprint(ServePort),
			Help:    "The Tailcat port this machine serves WebDAV on.",
			Check:   connector.CheckPort,
		},
		{
			Key:     "folder",
			Label:   "Shared folder",
			Default: share.HomeVolumeRoot(),
			Help:    "The folder this machine shares. Its contents are readable by every allowed host.",
			Check:   checkFolder,
		},
	}
}

// ClientFields are the settings of the WebDAV port on one host.
func (c *Connector) ClientFields() []connector.Field {
	return []connector.Field{{
		Key:     "port",
		Label:   "Host's WebDAV port",
		Default: fmt.Sprint(ServePort),
		Help:    "The port the host serves WebDAV on.",
		Check:   connector.CheckPort,
	}}
}

// checkFolder accepts an existing directory, which is what the share serves.
func checkFolder(folder string) error {
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%q is not an existing folder", folder)
	}
	return nil
}

// Listen serves the shared folder in cfg as WebDAV on the Tailcat port in cfg.
func (c *Connector) Listen(cfg connector.Config) error {
	if err := checkFolder(cfg["folder"]); err != nil {
		return err
	}
	port, err := cfg.Port("port")
	if err != nil {
		return err
	}
	handler := newHandler(xwebdav.Dir(cfg["folder"]), c.logf)
	if err := c.ports.Claim(port, serveHTTPOnConn(handler)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.port = port
	return nil
}

// Stop stops serving WebDAV on the port Listen claimed.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port != 0 {
		c.ports.Unregister(c.port)
		c.port = 0
	}
	return nil
}

// Healthy reports whether this machine serves WebDAV and can read everything it
// serves, which on macOS needs Full Disk Access (see connector.DiskAccess).
func (c *Connector) Healthy() bool {
	c.mu.Lock()
	port := c.port
	c.mu.Unlock()
	return port != 0 && c.ports.Registered(port) && c.access.Check()
}

// Connect tunnels to the host's WebDAV port in cfg, and copies the share's
// address. The file manager is opened in the background, so Connect returns as
// soon as the tunnel is open: a slow or failing file manager must not hold up
// the caller. If it cannot be started, onError reports why.
func (c *Connector) Connect(host state.Host, cfg connector.Config, onError func(error)) (connector.Client, error) {
	info, err := connector.ParseHost(host.Address)
	if err != nil {
		return nil, err
	}
	port, err := cfg.Port("port")
	if err != nil {
		return nil, err
	}
	client := c.dialing.Dialer(info.Addr())
	// Check that the host serves WebDAV here, so Connect fails with a reason
	// instead of the file manager failing later.
	ctx, cancel := context.WithTimeout(context.Background(), share.DialTimeout)
	probe, err := client.DialTCPPort(ctx, port)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("host does not serve WebDAV on port %d (it must be a Mochi host with its WebDAV switch on, and this client must be allowed): %w", port, err)
	}
	probe.Close()

	t, err := connector.OpenTunnel(client, port)
	if err != nil {
		return nil, err
	}
	address := share.Address(runtime.GOOS, t.Port())
	c.copyText(address)
	go func() {
		if err := share.Open(runtime.GOOS, address); err != nil {
			onError(fmt.Errorf("open file manager: %w", err))
		}
	}()
	return t, nil
}
