package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/tailcat"
	"golang.org/x/net/webdav"
	"tailscale.com/tstest/integration"
	"tailscale.com/types/key"
)

// newTestHost returns a host's WebDAV service over a temporary root that holds
// a.txt, and the root.
func newTestHost(t *testing.T) (*webdavServer, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &webdavServer{fs: webdav.Dir(root), locks: webdav.NewMemLS(), logf: t.Logf}, root
}

// pipeDialer reaches h over an in-process pipe, as a Tailcat connection would.
func pipeDialer(h http.Handler) func(context.Context) (net.Conn, error) {
	serve := serveHTTPOnConn(h)
	return func(context.Context) (net.Conn, error) {
		local, remote := net.Pipe()
		go serve(remote)
		return local, nil
	}
}

func doHTTP(t *testing.T, method, target, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// checkShare drives a share the way Explorer does: reads, a listing, a write,
// and a move whose Destination carries the share path.
func checkShare(t *testing.T, link *webdavLink, root string) {
	t.Helper()
	base := link.url
	addr := link.listener.Addr().String()
	prefix := strings.TrimSuffix(strings.TrimPrefix(base, "http://"+addr), "/")

	if code, body := doHTTP(t, "GET", base+"a.txt", "", nil); code != 200 || body != "hi" {
		t.Fatalf("GET a.txt = %d %q; want 200 \"hi\"", code, body)
	}
	if code, _ := doHTTP(t, "GET", "http://"+addr+"/a.txt", "", nil); code != 404 {
		t.Fatalf("GET without the share path = %d; want 404", code)
	}
	if code, body := doHTTP(t, "PROPFIND", base, "", map[string]string{"Depth": "1"}); code != 207 || !strings.Contains(body, prefix+"/a.txt") {
		t.Fatalf("PROPFIND = %d %s; want 207 with hrefs under %s", code, body, prefix)
	}
	if code, _ := doHTTP(t, "PROPFIND", "http://"+addr+"/", "", map[string]string{"Depth": "0"}); code != 207 {
		t.Fatalf("root probe = %d; want 207", code)
	}
	if code, _ := doHTTP(t, "PUT", base+"b.txt", "new", nil); code != 201 && code != 204 {
		t.Fatalf("PUT = %d", code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(got) != "new" {
		t.Fatalf("host file = %q; want \"new\"", got)
	}
	if code, _ := doHTTP(t, "MOVE", base+"b.txt", "", map[string]string{"Destination": base + "c.txt"}); code != 201 && code != 204 {
		t.Fatalf("MOVE = %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("MOVE left the source in place")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "c.txt")); string(got) != "new" {
		t.Fatalf("moved file = %q; want \"new\"", got)
	}
}

// The share relays to the host's WebDAV service: paths, listings, writes, and
// moves all land on the host's disk, and nothing answers without the share path.
func TestWebDAVLinkRelaysHostService(t *testing.T) {
	host, root := newTestHost(t)
	link, err := startWebDAVLink(pipeDialer(host), t.Logf, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.close)
	checkShare(t, link, root)
}

// The same share over a real Tailcat tunnel, to a server that serves the
// WebDAV port as a Mochi host does.
func TestWebDAVOverTailcat(t *testing.T) {
	t.Setenv("TS_DEBUG_ALWAYS_USE_DERP", "1")
	dm := integration.RunDERPAndSTUN(t, quiet, "127.0.0.1")
	host, root := newTestHost(t)
	serve := serveHTTPOnConn(host)
	s := &tailcat.Server{Region: dm.Regions[1], Logf: quiet}
	s.OnTCP = func(port uint16) func(net.Conn) {
		if port != webdavServePort {
			return nil
		}
		return serve
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	d := NewDialer(s.TailcatAddr(), key.NewNode(), nil, quiet)
	t.Cleanup(func() { d.Close() })
	dial := func(ctx context.Context) (net.Conn, error) { return d.DialTCPPort(ctx, webdavServePort) }
	link, err := startWebDAVLink(dial, t.Logf, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.close)
	checkShare(t, link, root)
}
