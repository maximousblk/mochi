package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// webdavVerbose logs every request, not only errors. Set by --verbose.
var webdavVerbose bool

const (
	webdavDialTimeout = 30 * time.Second
	webdavLogName     = "webdav.log"
)

// homeVolumeRoot is the root of the volume holding this user's home directory,
// the reach a shell session already has. It is "" if the home directory is unknown.
func homeVolumeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.VolumeName(home) + string(filepath.Separator)
}

// shareAddress is the address Mochi copies for a share. Windows Explorer mounts
// WebDAV from a UNC path, which names the loopback port with "@port". macOS
// Finder and other platforms use the http URL.
func shareAddress(goos string, port int, token, httpURL string) string {
	if goos == "windows" {
		return fmt.Sprintf(`\\127.0.0.1@%d\DavWWWRoot\%s`, port, token)
	}
	return httpURL
}

// openShare opens the share in Explorer on Windows and in Finder on macOS. Other
// platforms have no file manager to open, so the address is only copied.
func openShare(goos, address, httpURL string) error {
	switch goos {
	case "windows":
		return startProcess("explorer.exe", address)
	case "darwin":
		return startProcess("osascript", "-e", `mount volume "`+httpURL+`"`)
	}
	return nil
}

// openWebDAVLog opens webdav.log beside the state database, creating it if needed.
func openWebDAVLog() (*os.File, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(filepath.Dir(p), webdavLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// requirePrefix rejects requests outside the secret share path, so only the
// address Mochi displayed reaches the share.
func requirePrefix(prefix string, next http.Handler) http.Handler {
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

// logRequests logs each request's method, path, Depth, client, and status
// through logf when webdavVerbose is set.
func logRequests(logf func(format string, args ...any), prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if webdavVerbose {
			logf("%s %s depth=%q agent=%q -> %d (%v)",
				r.Method, redactPath(r.URL.Path, prefix), r.Header.Get("Depth"), r.UserAgent(), rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// redactPath replaces the share token in p with <share>.
func redactPath(p, prefix string) string {
	if prefix != "" && strings.HasPrefix(p, prefix) {
		return "/<share>" + strings.TrimPrefix(p, prefix)
	}
	return p
}
