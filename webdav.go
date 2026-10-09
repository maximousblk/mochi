package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"
	"golang.org/x/net/webdav"
)

// The WebDAV launcher mounts a Mochi host's WebDAV service. The host runs a
// WebDAV server inside Mochi that serves its home volume read-write, with its
// own locks, so Explorer and Finder speak WebDAV end to end. This machine
// listens on 127.0.0.1 at an unguessable path and proxies each request over the
// Tailcat tunnel unchanged. Only Mochi hosts serve WebDAV; a stock tailcat does
// not.
//
// Errors are logged to webdav.log beside the state database, as for Files.

//go:embed resources/webdav.svg
var webdavIconData []byte

var webdavIcon = ui.MustParseSVG(webdavIconData)

// webdavServePort is the Tailcat port a Mochi host serves WebDAV on.
const webdavServePort uint16 = 8080

// webdavPrefixHeader carries a share's secret path from the loopback proxy to
// the host, which builds hrefs and checks Destination headers under it. Only
// the proxy sets it.
const webdavPrefixHeader = "X-Mochi-Share-Prefix"

// webdavServer is the WebDAV service a Mochi host offers. It uses webdav's own
// handler, so locks, ETags, and PROPFIND behave as the filesystem defines them.
type webdavServer struct {
	fs    webdav.FileSystem
	locks webdav.LockSystem
	logf  func(format string, args ...any)
}

func (s *webdavServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := r.Header.Get(webdavPrefixHeader)
	h := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: s.fs,
		LockSystem: s.locks,
		Logger: func(req *http.Request, err error) {
			if err != nil {
				s.logf("error %s %s: %v", req.Method, redactPath(req.URL.Path, prefix), err)
			}
		},
	}
	logRequests(s.logf, prefix, h).ServeHTTP(w, r)
}

// lazyLog opens webdav.log on its first message, so a host that never fails
// writes no file.
type lazyLog struct {
	once sync.Once
	log  *log.Logger
}

func (l *lazyLog) Printf(format string, args ...any) {
	l.once.Do(func() {
		f, err := openWebDAVLog()
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
			ConnState: func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed || state == http.StateHijacked {
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

// WebDAVConnector mounts each enabled Mochi host's WebDAV service on loopback.
type WebDAVConnector struct {
	icon    *ui.SVG
	dialing Dialing
	shares  map[string]*webdavLink
	// server is nil if this machine has no home volume to serve.
	server *webdavServer
	serverSwitch
}

func NewWebDAVConnector(icon *ui.SVG, dialing Dialing) *WebDAVConnector {
	c := &WebDAVConnector{icon: icon, dialing: dialing, shares: map[string]*webdavLink{}}
	if root := homeVolumeRoot(); root != "" {
		logger := &lazyLog{}
		c.server = &webdavServer{fs: webdav.Dir(root), locks: webdav.NewMemLS(), logf: logger.Printf}
	}
	return c
}

func (c *WebDAVConnector) Name() string       { return "WebDAV" }
func (c *WebDAVConnector) Icon() *ui.SVG      { return c.icon }
func (c *WebDAVConnector) SetHosts([]Host)    {}
func (c *WebDAVConnector) CLICommand() string { return "" }

// ServerServe serves this machine's home volume as WebDAV on webdavServePort.
func (c *WebDAVConnector) ServerServe(sv *ServerServices) {
	if c.server != nil {
		sv.Serve[webdavServePort] = serveHTTPOnConn(c.server)
	}
}

func (c *WebDAVConnector) Enabled(host Host) bool { _, ok := c.shares[host.Address]; return ok }

func (c *WebDAVConnector) RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error {
	return errors.New("webdav has no command-line mode")
}

// URL returns the share's address while enabled, or "" otherwise.
func (c *WebDAVConnector) URL(host Host) string {
	if s, ok := c.shares[host.Address]; ok {
		return s.address
	}
	return ""
}

// Enable connects to the host, starts the loopback share, and opens it in the
// platform file manager. If the file manager cannot be started, the share is
// closed again.
func (c *WebDAVConnector) Enable(host Host) error {
	if c.Enabled(host) {
		return nil
	}
	l, err := openWebDAVLink(c.dialing, host.Address)
	if err != nil {
		return err
	}
	if err := openShare(runtime.GOOS, l.address, l.url); err != nil {
		l.close()
		return fmt.Errorf("open file manager: %w", err)
	}
	c.shares[host.Address] = l
	return nil
}

// Disable closes the share for host. It is idempotent, and it never fails:
// the host's cleanup must not be blocked by a connection that is already gone.
func (c *WebDAVConnector) Disable(host Host) error {
	l, ok := c.shares[host.Address]
	if !ok {
		return nil
	}
	delete(c.shares, host.Address)
	l.close()
	return nil
}

// webdavLink is one host's share: a loopback listener whose requests are
// proxied to the host's WebDAV service.
type webdavLink struct {
	url       string
	address   string
	listener  net.Listener
	server    *http.Server
	transport *http.Transport
	release   func()
}

func (l *webdavLink) close() {
	l.server.Close()
	l.listener.Close()
	l.transport.CloseIdleConnections()
	l.release()
}

// openWebDAVLink connects to the host's WebDAV service and serves it on loopback.
func openWebDAVLink(dialing Dialing, address string) (*webdavLink, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return nil, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := dialing.Dialer(info.Addr())
	// Check that the host serves WebDAV here, so Enable fails with a reason
	// instead of the file manager failing later.
	ctx, cancel := context.WithTimeout(context.Background(), webdavDialTimeout)
	probe, err := client.DialTCPPort(ctx, webdavServePort)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("host does not serve WebDAV on port %d (it must be a Mochi host with its WebDAV switch on, and this client must be allowed): %w", webdavServePort, err)
	}
	probe.Close()

	logFile, err := openWebDAVLog()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("open WebDAV log: %w", err)
	}
	logger := log.New(logFile, "", log.LstdFlags)
	dial := func(ctx context.Context) (net.Conn, error) { return client.DialTCPPort(ctx, webdavServePort) }
	l, err := startWebDAVLink(dial, logger.Printf, func() {
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

// startWebDAVLink serves the host's WebDAV service, reached through dial, on a
// loopback address under a fresh secret path. release runs once the link closes.
func startWebDAVLink(dial func(context.Context) (net.Conn, error), logf func(format string, args ...any), release func()) (*webdavLink, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
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
			r.Header.Set(webdavPrefixHeader, prefix)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logf("proxy %s %s: %v", r.Method, redactPath(r.URL.Path, prefix), err)
			http.Error(w, "the host did not answer", http.StatusBadGateway)
		},
	}
	server := &http.Server{
		Handler:           logRequests(logf, prefix, requirePrefix(prefix, proxy)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go server.Serve(ln)
	return &webdavLink{
		url:       httpURL,
		address:   shareAddress(runtime.GOOS, ln.Addr().(*net.TCPAddr).Port, token, httpURL),
		listener:  ln,
		server:    server,
		transport: transport,
		release:   release,
	}, nil
}
