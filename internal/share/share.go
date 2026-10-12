// Package share holds what the WebDAV launcher's client and server have in
// common: the address Mochi copies, the Explorer and Finder hand-off, request
// logging to webdav.log, and starting the RustDesk client.
package share

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ashwin-y/mochi/internal/state"
)

// Verbose logs every request, not only errors. Set by --verbose.
var Verbose bool

// DialTimeout bounds how long a WebDAV connection waits for the host to accept it.
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

// Address is the address Mochi copies for a WebDAV port on this machine.
// Windows Explorer mounts WebDAV from a UNC path, which names the loopback port
// with "@port". macOS Finder and other platforms use the http URL.
func Address(goos string, port int) string {
	if goos == "windows" {
		return fmt.Sprintf(`\\127.0.0.1@%d\DavWWWRoot`, port)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

// Open opens address in Explorer on Windows and in Finder on macOS. Other
// platforms have no file manager to open, so the address is only copied.
func Open(goos, address string) error {
	switch goos {
	case "windows":
		return StartProcess("explorer.exe", address)
	case "darwin":
		return StartProcess("osascript", "-e", `mount volume "`+address+`"`)
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
func LogRequests(logf func(format string, args ...any), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if Verbose {
			logf("%s %s depth=%q agent=%q -> %d (%v)",
				r.Method, r.URL.Path, r.Header.Get("Depth"), r.UserAgent(), rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}
