// Package webdav is the WebDAV launcher. A Mochi host runs a WebDAV server
// that serves its home volume read-write, with its own locks, so Explorer and
// Finder speak WebDAV end to end. This machine listens on 127.0.0.1 at an
// unguessable path and proxies each request over the Tailcat tunnel unchanged.
// Only Mochi hosts serve WebDAV; a stock tailcat does not.
//
// Errors are logged to webdav.log beside the state database, as for Files.
package webdav

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
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
	"github.com/tailscale/tailcat"
	xwebdav "golang.org/x/net/webdav"
)

// ServePort is the Tailcat port a Mochi host serves WebDAV on.
const ServePort uint16 = 8080

// prefixHeader carries a share's secret path from the loopback proxy to the
// host, which builds hrefs and checks Destination headers under it. Only the
// proxy sets it.
const prefixHeader = "X-Mochi-Share-Prefix"

// server is the WebDAV service a Mochi host offers. It uses webdav's own
// handler, so locks, ETags, and PROPFIND behave as the filesystem defines them.
type server struct {
	fs    xwebdav.FileSystem
	locks xwebdav.LockSystem
	logf  func(format string, args ...any)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := r.Header.Get(prefixHeader)
	h := &xwebdav.Handler{
		Prefix:     prefix,
		FileSystem: s.fs,
		LockSystem: s.locks,
		Logger: func(req *http.Request, err error) {
			if err != nil {
				s.logf("error %s %s: %v", req.Method, share.RedactPath(req.URL.Path, prefix), err)
			}
		},
	}
	share.LogRequests(s.logf, prefix, h).ServeHTTP(w, r)
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

// Connector mounts each enabled Mochi host's WebDAV service on loopback.
type Connector struct {
	icon    *ui.SVG
	dialing admission.Dialing
	shares  map[string]*link
	// server is nil if this machine has no home volume to serve.
	server *server
	connector.ServerSwitch
}

// New returns the WebDAV launcher. icon is its tile icon.
func New(icon *ui.SVG, dialing admission.Dialing) *Connector {
	c := &Connector{icon: icon, dialing: dialing, shares: map[string]*link{}}
	if root := share.HomeVolumeRoot(); root != "" {
		logger := &lazyLog{}
		c.server = &server{fs: xwebdav.Dir(root), locks: xwebdav.NewMemLS(), logf: logger.Printf}
	}
	return c
}

func (c *Connector) Name() string                 { return "WebDAV" }
func (c *Connector) Icon() *ui.SVG                { return c.icon }
func (c *Connector) SetHosts([]state.Host)        {}
func (c *Connector) Enabled(host state.Host) bool { _, ok := c.shares[host.Address]; return ok }

// ServerServe serves this machine's home volume as WebDAV on ServePort.
func (c *Connector) ServerServe(sv *connector.ServerServices) {
	if c.server != nil {
		sv.Serve[ServePort] = serveHTTPOnConn(c.server)
	}
}

// URL returns the share's address while enabled, or "" otherwise.
func (c *Connector) URL(host state.Host) string {
	if s, ok := c.shares[host.Address]; ok {
		return s.address
	}
	return ""
}

// Connect connects to the host, starts the loopback share, and opens it in the
// platform file manager. If the file manager cannot be started, the share is
// closed again.
func (c *Connector) Connect(host state.Host) error {
	if c.Enabled(host) {
		return nil
	}
	l, err := openLink(c.dialing, host.Address)
	if err != nil {
		return err
	}
	if err := share.Open(runtime.GOOS, l.address, l.url); err != nil {
		l.close()
		return fmt.Errorf("open file manager: %w", err)
	}
	c.shares[host.Address] = l
	return nil
}

// Disconnect closes the share for host. It is idempotent, and it never fails:
// the host's cleanup must not be blocked by a connection that is already gone.
func (c *Connector) Disconnect(host state.Host) error {
	l, ok := c.shares[host.Address]
	if !ok {
		return nil
	}
	delete(c.shares, host.Address)
	l.close()
	return nil
}

// link is one host's share: a loopback listener whose requests are proxied to
// the host's WebDAV service.
type link struct {
	url       string
	address   string
	listener  net.Listener
	server    *http.Server
	transport *http.Transport
	release   func()
}

func (l *link) close() {
	l.server.Close()
	l.listener.Close()
	l.transport.CloseIdleConnections()
	l.release()
}

// openLink connects to the host's WebDAV service and serves it on loopback.
func openLink(dialing admission.Dialing, address string) (*link, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return nil, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := dialing.Dialer(info.Addr())
	// Check that the host serves WebDAV here, so Enable fails with a reason
	// instead of the file manager failing later.
	ctx, cancel := context.WithTimeout(context.Background(), share.DialTimeout)
	probe, err := client.DialTCPPort(ctx, ServePort)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("host does not serve WebDAV on port %d (it must be a Mochi host with its WebDAV switch on, and this client must be allowed): %w", ServePort, err)
	}
	probe.Close()

	logFile, err := share.OpenLog()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("open WebDAV log: %w", err)
	}
	logger := log.New(logFile, "", log.LstdFlags)
	dial := func(ctx context.Context) (net.Conn, error) { return client.DialTCPPort(ctx, ServePort) }
	l, err := startLink(dial, logger.Printf, func() {
		client.Close()
		logFile.Close()
	})
	if err != nil {
		client.Close()
		logFile.Close()
		return nil, err
	}
	return l, nil
}

// startLink serves the host's WebDAV service, reached through dial, on a
// loopback address under a fresh secret path. release runs once the link closes.
func startLink(dial func(context.Context) (net.Conn, error), logf func(format string, args ...any), release func()) (*link, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token, err := share.RandomToken()
	if err != nil {
		ln.Close()
		return nil, err
	}
	prefix := "/" + token
	httpURL := fmt.Sprintf("http://%s%s/", ln.Addr().String(), prefix)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx)
		},
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	proxy := &httputil.ReverseProxy{
		// The request keeps its path, Host, and Destination, so the host can
		// build hrefs under the share path. The host learns that path from the
		// header.
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "mochi-host"
			r.Header.Set(prefixHeader, prefix)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logf("proxy %s %s: %v", r.Method, share.RedactPath(r.URL.Path, prefix), err)
			http.Error(w, "the host did not answer", http.StatusBadGateway)
		},
	}
	srv := &http.Server{
		Handler:           share.LogRequests(logf, prefix, share.RequirePrefix(prefix, proxy)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)
	return &link{
		url:       httpURL,
		address:   share.Address(runtime.GOOS, ln.Addr().(*net.TCPAddr).Port, token, httpURL),
		listener:  ln,
		server:    srv,
		transport: transport,
		release:   release,
	}, nil
}
