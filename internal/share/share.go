// Package share holds what the loopback shares of the Files and WebDAV
// launchers have in common: the address Mochi copies, the secret path guard,
// request logging and the Explorer and Finder hand-off.
package share

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashwin-y/mochi/internal/state"
)

// Verbose logs every request, not only errors. Set by --verbose.
var Verbose bool

// DialTimeout bounds how long a share waits for the host to accept a connection.
const DialTimeout = 30 * time.Second

const logName = "webdav.log"

// HomeVolumeRoot is the root of the volume holding this user's home directory,
// the reach a shell session already has. It is "" if the home directory is unknown.
func HomeVolumeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.VolumeName(home) + string(filepath.Separator)
}

// Address is the address Mochi copies for a share. Windows Explorer mounts
// WebDAV from a UNC path, which names the loopback port with "@port". macOS
// Finder and other platforms use the http URL.
func Address(goos string, port int, token, httpURL string) string {
	if goos == "windows" {
		return fmt.Sprintf(`\\127.0.0.1@%d\DavWWWRoot\%s`, port, token)
	}
	return httpURL
}

// Open opens the share in Explorer on Windows and in Finder on macOS. Other
// platforms have no file manager to open, so the address is only copied.
func Open(goos, address, httpURL string) error {
	switch goos {
	case "windows":
		return StartProcess("explorer.exe", address)
	case "darwin":
		return StartProcess("osascript", "-e", `mount volume "`+httpURL+`"`)
	}
	return nil
}

// StartProcess starts exe with args and does not wait for it to exit.
func StartProcess(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// OpenLog opens webdav.log beside the state database, creating it if needed.
func OpenLog() (*os.File, error) {
	p, err := state.Path()
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(filepath.Dir(p), logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}

// RandomToken returns an unguessable path segment for a share.
func RandomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RequirePrefix rejects requests outside the secret share path, so only the
// address Mochi displayed reaches the share.
func RequirePrefix(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			serveRootProbe(w, r)
			return
		}
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const rootMultistatus = `<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`

// serveRootProbe answers the server root, which Windows' WebDAV redirector
// probes before it reaches the share path in a \\127.0.0.1@port\DavWWWRoot\<token>
// UNC path. Without an answer there, Explorer reports error 0x80004005. The root
// is an empty collection, so it reveals nothing about the share.
func serveRootProbe(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "OPTIONS":
		w.Header().Set("DAV", "1, 2")
		w.Header().Set("MS-Author-Via", "DAV")
		w.WriteHeader(http.StatusOK)
	case "PROPFIND":
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, rootMultistatus)
	default:
		http.NotFound(w, r)
	}
}

// statusRecorder keeps the status code a handler wrote, for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}

// LogRequests logs each request's method, path, Depth, client, and status
// through logf when Verbose is set.
func LogRequests(logf func(format string, args ...any), prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if Verbose {
			logf("%s %s depth=%q agent=%q -> %d (%v)",
				r.Method, RedactPath(r.URL.Path, prefix), r.Header.Get("Depth"), r.UserAgent(), rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// RedactPath replaces the share token in p with <share>.
func RedactPath(p, prefix string) string {
	if prefix != "" && strings.HasPrefix(p, prefix) {
		return "/<share>" + strings.TrimPrefix(p, prefix)
	}
	return p
}
