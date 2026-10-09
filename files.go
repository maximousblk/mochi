package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
	"github.com/tailscale/tailcat"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/net/webdav"
)

// The Files launcher exposes a host's files as a WebDAV share on 127.0.0.1, so
// Windows Explorer and macOS Finder can mount it with their built-in clients.
// The host serves SFTP on port 22: a stock tailcat run with --files, or a Mochi
// host. Each WebDAV request becomes SFTP calls on the host (webdavfs.go).
// Enabling it connects to the host and opens the share in Explorer (Windows) or
// Finder (macOS). Disabling it tears the connection down.
//
// Errors are always logged to webdav.log beside the state database. With
// --verbose, every request is logged too. The share token is replaced with
// <share> in the log so the log does not hold the share's bearer path.

type filesShare struct {
	url      string
	address  string
	listener net.Listener
	server   *http.Server
	client   *Dialer
	ssh      *gossh.Client
	sftp     *sftp.Client
	logFile  *os.File
}

// FilesConnector serves each enabled host's files as a loopback WebDAV share,
// translated to SFTP.
type FilesConnector struct {
	icon    *ui.SVG
	dialing Dialing
	shares  map[string]*filesShare
	serverSwitch
}

func NewFilesConnector(icon *ui.SVG, dialing Dialing) *FilesConnector {
	c := &FilesConnector{icon: icon, dialing: dialing, shares: map[string]*filesShare{}}
	c.SetServerEnabled(true)
	return c
}

func (c *FilesConnector) Name() string       { return "Files" }
func (c *FilesConnector) Icon() *ui.SVG      { return c.icon }
func (c *FilesConnector) SetHosts([]Host)    {}
func (c *FilesConnector) CLICommand() string { return "" }

// ServerServe serves this machine's files over SFTP, read-write, from the root
// of its home volume: the reach a shell session already has.
func (c *FilesConnector) ServerServe(sv *ServerServices) {
	if root := homeVolumeRoot(); root != "" {
		sv.Files = &tailcat.FileService{Dir: root, Mode: tailcat.FileServeRW}
	}
}

func (c *FilesConnector) Enabled(host Host) bool { _, ok := c.shares[host.Address]; return ok }

func (c *FilesConnector) RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error {
	return errors.New("files has no command-line mode")
}

// URL returns the share's address while enabled, or "" otherwise.
func (c *FilesConnector) URL(host Host) string {
	if s, ok := c.shares[host.Address]; ok {
		return s.address
	}
	return ""
}

// Enable connects to the host, starts serving its files on loopback, and opens
// the share in the platform file manager. If the file manager cannot be
// started, the share is closed again.
func (c *FilesConnector) Enable(host Host) error {
	if c.Enabled(host) {
		return nil
	}
	s, err := openFilesShare(c.dialing, host.Address)
	if err != nil {
		return err
	}
	go s.server.Serve(s.listener)
	if err := openShare(runtime.GOOS, s.address, s.url); err != nil {
		s.close()
		return fmt.Errorf("open file manager: %w", err)
	}
	c.shares[host.Address] = s
	return nil
}

// Disable closes the share for host. It is idempotent, and it never fails:
// the host's cleanup must not be blocked by a connection that is already gone.
func (c *FilesConnector) Disable(host Host) error {
	s, ok := c.shares[host.Address]
	if !ok {
		return nil
	}
	delete(c.shares, host.Address)
	s.close()
	return nil
}

func openFilesShare(dialing Dialing, address string) (*filesShare, error) {
	info, err := tailcat.ParseAddr(tailcat.Addr(address))
	if err != nil {
		return nil, fmt.Errorf("invalid Tailcat address: %w", err)
	}
	client := dialing.Dialer(info.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), webdavDialTimeout)
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
	logFile, err := openWebDAVLog()
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
	token, err := randomToken()
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
				logger.Printf("error %s %s: %v", r.Method, redactPath(r.URL.Path, prefix), err)
			}
		},
	}
	return &filesShare{
		url:      httpURL,
		address:  shareAddress(runtime.GOOS, ln.Addr().(*net.TCPAddr).Port, token, httpURL),
		listener: ln,
		server: &http.Server{
			Handler:           logRequests(logger.Printf, prefix, requirePrefix(prefix, handler)),
			ReadHeaderTimeout: 10 * time.Second,
		},
		client:  client,
		ssh:     sc,
		sftp:    sf,
		logFile: logFile,
	}, nil
}

// close releases the share's listener, SFTP session, SSH connection, tunnel, and log.
func (s *filesShare) close() {
	s.server.Close()
	s.listener.Close()
	s.sftp.Close()
	s.ssh.Close()
	s.client.Close()
	s.logFile.Close()
}
