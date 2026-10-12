package app

import (
	"path/filepath"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/connector/files"
	"github.com/ashwin-y/mochi/internal/connector/rustdesk"
	"github.com/ashwin-y/mochi/internal/connector/ssh"
	"github.com/ashwin-y/mochi/internal/connector/webdav"
	"github.com/tailscale/tailcat"
)

// The server's port 22 and WebDAV port follow the connectors' server-side
// switches as they change, with no restart. Files is on by default and so is
// SSH, so port 22 is open; WebDAV and RustDesk are off. RustDesk's switch needs
// a real RustDesk, so its Listen is tested in its own package.
func TestServerHandlerFollowsSwitches(t *testing.T) {
	sshC := ssh.New(filepath.Join(t.TempDir(), "config"), sshIcon)
	filesC := files.New(folderIcon, admission.Dialing{})
	dav := webdav.New(webdavIcon, admission.Dialing{})
	rd := rustdesk.New(rustdeskIcon, admission.Dialing{})
	a := &app{connectors: []connector.Connector{sshC, filesC, dav, rd}}
	s := &tailcat.Server{}

	if a.serverHandler(s, 22) == nil {
		t.Fatal("port 22 closed while SSH and Files are on by default")
	}
	if a.serverHandler(s, webdav.ServePort) != nil {
		t.Fatal("WebDAV port open while its server side is off")
	}
	if a.serverHandler(s, rustdesk.DirectPort) != nil {
		t.Fatal("RustDesk port open while its server side is off")
	}

	dav.Listen()
	if a.serverHandler(s, webdav.ServePort) == nil {
		t.Fatal("WebDAV port closed after its server side was turned on")
	}

	sshC.StopListening()
	if a.serverHandler(s, 22) == nil {
		t.Fatal("port 22 closed while Files still serves files")
	}
	filesC.StopListening()
	if a.serverHandler(s, 22) != nil {
		t.Fatal("port 22 open with SSH and Files both off")
	}
	if a.serverHandler(s, webdav.ServePort) == nil {
		t.Fatal("turning Files off closed the WebDAV port")
	}
}
