// Package files is the Files launcher. It exposes a host's files as a WebDAV
// share on 127.0.0.1, so Windows Explorer and macOS Finder can mount it with
// their built-in clients. The host serves SFTP on port 22: a stock tailcat run
// with --files, or a Mochi host. Each WebDAV request becomes SFTP calls on the
// host (sftpfs.go). Enabling it connects to the host and opens the share in
// Explorer (Windows) or Finder (macOS). Disabling it tears the connection down.
//
// Errors are always logged to webdav.log beside the state database. With
// --verbose, every request is logged too. The share token is replaced with
// <share> in the log so the log does not hold the share's bearer path.
package files

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/share"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
	"github.com/tailscale/tailcat"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/net/webdav"
)

type fileShare struct {
	url      string
	address  string
	listener net.Listener
	server   *http.Server
	client   *admission.Dialer
	ssh      *gossh.Client
	sftp     *sftp.Client
	logFile  *os.File
}

// Connector serves each enabled host's files as a loopback WebDAV share,
// translated to SFTP.
type Connector struct {
	icon    *ui.SVG
	dialing admission.Dialing
	shares  map[string]*fileShare
	connector.ServerSwitch
}

// New returns the Files launcher. icon is its tile icon.
func New(icon *ui.SVG, dialing admission.Dialing) *Connector {
	c := &Connector{icon: icon, dialing: dialing, shares: map[string]*fileShare{}}
	c.SetServerEnabled(true)
	return c
}

func (c *Connector) Name() string                 { return "Files" }
func (c *Connector) Icon() *ui.SVG                { return c.icon }
func (c *Connector) SetHosts([]state.Host)        {}
func (c *Connector) Enabled(host state.Host) bool { _, ok := c.shares[host.Address]; return ok }

// ServerServe serves this machine's files over SFTP, read-write, from the root
// of its home volume: the reach a shell session already has.
func (c *Connector) ServerServe(sv *connector.ServerServices) {
	if root := share.HomeVolumeRoot(); root != "" {
		sv.Files = &tailcat.FileService{Dir: root, Mode: tailcat.FileServeRW}
	}
}

// URL returns the share's address while enabled, or "" otherwise.
func (c *Connector) URL(host state.Host) string {
	if s, ok := c.shares[host.Address]; ok {
		return s.address
	}
	return ""
}

// Enable connects to the host, starts serving its files on loopback, and opens
// the share in the platform file manager. If the file manager cannot be
// started, the share is closed again.
func (c *Connector) Enable(host state.Host) error {
	if c.Enabled(host) {
		return nil
	}
	s, err := openShare(c.dialing, host.Address)
	if err != nil {
		return err
	}
	go s.server.Serve(s.listener)
	if err := share.Open(runtime.GOOS, s.address, s.url); err != nil {
		s.close()
		return fmt.Errorf("open file manager: %w", err)
	}
	c.shares[host.Address] = s
	return nil
}

// Disable closes the share for host. It is idempotent, and it never fails:
// the host's cleanup must not be blocked by a connection that is already gone.
func (c *Connector) Disable(host state.Host) error {
	s, ok := c.shares[host.Address]
	if !ok {
		return nil
	}
	delete(c.shares, host.Address)
	s.close()
	return nil
}

func openShare(dialing admission.Dialing, address string) (*fileShare, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return nil, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := dialing.Dialer(info.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), share.DialTimeout)
	defer cancel()
	conn, err := client.DialTCPPort(ctx, 22)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("connect to files service: %w", err)
	}
	// The tunnel already authenticated the server by its node key, so the SSH
	// host key adds nothing. The files service takes no SSH credentials.
	sshConn, chans, reqs, err := gossh.NewClientConn(conn, "tailcat", &gossh.ClientConfig{
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		conn.Close()
		client.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	sc := gossh.NewClient(sshConn, chans, reqs)
	sf, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		client.Close()
		return nil, fmt.Errorf("open SFTP session: %w", err)
	}

	// closeAll releases everything opened so far, on any failure below.
	closeAll := func() {
		sf.Close()
		sc.Close()
		client.Close()
	}
	logFile, err := share.OpenLog()
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("open WebDAV log: %w", err)
	}
	logger := log.New(logFile, "", log.LstdFlags)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logFile.Close()
		closeAll()
		return nil, err
	}
	token, err := share.RandomToken()
	if err != nil {
		ln.Close()
		logFile.Close()
		closeAll()
		return nil, err
	}
	prefix := "/" + token
	httpURL := fmt.Sprintf("http://%s%s/", ln.Addr().String(), prefix)
	handler := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: &sftpFS{client: sf},
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				logger.Printf("error %s %s: %v", r.Method, share.RedactPath(r.URL.Path, prefix), err)
			}
		},
	}
	return &fileShare{
		url:      httpURL,
		address:  share.Address(runtime.GOOS, ln.Addr().(*net.TCPAddr).Port, token, httpURL),
		listener: ln,
		server: &http.Server{
			Handler:           share.LogRequests(logger.Printf, prefix, share.RequirePrefix(prefix, handler)),
			ReadHeaderTimeout: 10 * time.Second,
		},
		client:  client,
		ssh:     sc,
		sftp:    sf,
		logFile: logFile,
	}, nil
}

// close releases the share's listener, SFTP session, SSH connection, tunnel, and log.
func (s *fileShare) close() {
	s.server.Close()
	s.listener.Close()
	s.sftp.Close()
	s.ssh.Close()
	s.client.Close()
	s.logFile.Close()
}
