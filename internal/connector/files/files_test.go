package files

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/tailscale/tailcat"
	"tailscale.com/tstest/integration"
	"tailscale.com/types/key"
)

func quiet(string, ...any) {}

// A host serving a directory over SFTP on port 22, as a Mochi host does with
// its Files switch on, is mounted through a loopback WebDAV share that reads
// the same file.
func TestFilesShareOverTailcat(t *testing.T) {
	t.Setenv("TS_DEBUG_ALWAYS_USE_DERP", "1")
	dm := integration.RunDERPAndSTUN(t, quiet, "127.0.0.1")

	root := t.TempDir()
	const content = "shared over tailcat"
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &tailcat.Server{Region: dm.Regions[1], Logf: quiet}
	serveSSH := s.SSHConnHandler(tailcat.SSHOptions{
		Files: &tailcat.FileService{Dir: root, Mode: tailcat.FileServeRW},
	})
	s.OnTCP = func(port uint16) func(net.Conn) {
		if port != 22 {
			return nil
		}
		return serveSSH
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	dialing := admission.Dialing{Key: key.NewNode()}
	fs, err := openShare(dialing, string(s.TailcatAddr()))
	if err != nil {
		t.Fatalf("open share: %v", err)
	}
	t.Cleanup(fs.close)
	// Enable starts serving the share after opening it; do the same here.
	go fs.server.Serve(fs.listener)

	resp, err := http.Get(fs.url + "note.txt")
	if err != nil {
		t.Fatalf("GET through the share: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != content {
		t.Fatalf("GET note.txt = %d %q; want 200 %q", resp.StatusCode, body, content)
	}

	// Without the share's secret path the loopback listener answers nothing.
	bare, err := http.Get("http://" + fs.listener.Addr().String() + "/note.txt")
	if err != nil {
		t.Fatal(err)
	}
	bare.Body.Close()
	if bare.StatusCode != http.StatusNotFound {
		t.Fatalf("GET without the share path = %d; want 404", bare.StatusCode)
	}
}
