package main

import (
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/tailscale/tailcat"
)

// The server's port 22, WebDAV port, and RustDesk port follow the connectors'
// server-side switches as they change, with no restart. Files is on by default
// and so is SSH, so port 22 is open; WebDAV and RustDesk are off.
func TestServerHandlerFollowsSwitches(t *testing.T) {
	ssh := NewSSHConfigConnector(filepath.Join(t.TempDir(), "config"), sshIcon)
	files := NewFilesConnector(folderIcon, Dialing{})
	dav := NewWebDAVConnector(webdavIcon, Dialing{})
	rd := NewRustDeskConnector(rustdeskIcon, Dialing{})
	a := &app{connectors: []Connector{ssh, files, dav, rd}}
	s := &tailcat.Server{}

	if a.serverHandler(s, 22) == nil {
		t.Fatal("port 22 closed while SSH and Files are on by default")
	}
	if a.serverHandler(s, webdavServePort) != nil {
		t.Fatal("WebDAV port open while its server side is off")
	}
	if a.serverHandler(s, rustdeskDirectPort) != nil {
		t.Fatal("RustDesk port open while its server side is off")
	}

	dav.SetServerEnabled(true)
	if a.serverHandler(s, webdavServePort) == nil {
		t.Fatal("WebDAV port closed after its server side was turned on")
	}
	rd.SetServerEnabled(true)
	if a.serverHandler(s, rustdeskDirectPort) == nil {
		t.Fatal("RustDesk port closed after its server side was turned on")
	}

	ssh.SetServerEnabled(false)
	if a.serverHandler(s, 22) == nil {
		t.Fatal("port 22 closed while Files still serves files")
	}
	files.SetServerEnabled(false)
	if a.serverHandler(s, 22) != nil {
		t.Fatal("port 22 open with SSH and Files both off")
	}
	if a.serverHandler(s, webdavServePort) == nil {
		t.Fatal("turning Files off closed the WebDAV port")
	}
}

// A forwarded connection reaches the local address and its reply comes back.
func TestForwardToPipesBothWays(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err == nil {
			conn.Write(append([]byte("re:"), buf...))
		}
	}()

	remote, client := net.Pipe()
	go forwardTo(ln.Addr().String())(remote)
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 8)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if string(reply) != "re:hello" {
		t.Fatalf("reply = %q", reply)
	}
	client.Close()
}
