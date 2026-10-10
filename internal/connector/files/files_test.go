package files

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// A host serving a directory over SFTP on port 22, as a Mochi host does with
// its Files switch on, is mounted through a loopback WebDAV share that reads
// the same file.
func TestFilesShareOverTailcat(t *testing.T) {
	relay := testharness.Relay(t)

	root := t.TempDir()
	const content = "shared over tailcat"
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	host := testharness.Host(t, relay, map[uint16]func(net.Conn){
		22: (&tailcat.Server{}).SSHConnHandler(tailcat.SSHOptions{
			Files: &tailcat.FileService{Dir: root, Mode: tailcat.FileServeRW},
		}),
	})

	fs, err := openShare(admission.Dialing{Key: key.NewNode()}, testharness.Address(host))
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
