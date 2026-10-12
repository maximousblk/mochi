package rustdesk

import (
	"fmt"
	"net"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
)

// The server side turns on whether or not RustDesk accepts direct IP yet. It
// is healthy only while something accepts on RustDesk's direct-IP port, so it
// turns unhealthy, still on, when direct IP is off, and healthy again when it
// is turned on.
func TestHealthyFollowsDirectIPPort(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", DirectPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("port %d is in use on this machine: %v", DirectPort, err)
	}
	ln.Close() // RustDesk with direct IP off: nothing listens

	c := New(nil, admission.Dialing{})
	if c.Healthy() {
		t.Fatal("healthy while the server side is off")
	}
	if err := c.Listen(); err != nil {
		t.Fatalf("Listen with direct IP off: %v", err)
	}
	if !c.ServerEnabled() {
		t.Fatal("server side off after Listen")
	}
	if c.Healthy() {
		t.Fatal("healthy though RustDesk does not accept direct IP")
	}

	ln, err = net.Listen("tcp", addr) // the user turns direct IP on
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	if !c.Healthy() {
		t.Fatal("not healthy while RustDesk accepts direct IP")
	}

	ln.Close() // and off again
	if !c.ServerEnabled() {
		t.Fatal("the switch changed by itself")
	}
	if c.Healthy() {
		t.Fatal("healthy after RustDesk stopped accepting")
	}
}
