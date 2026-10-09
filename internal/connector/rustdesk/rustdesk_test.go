package rustdesk

import (
	"io"
	"net"
	"testing"
)

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
