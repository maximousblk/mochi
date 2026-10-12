package rustdesk

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
)

// Listen fails with a prompt while nothing accepts connections on RustDesk's
// direct-IP port, and the server side stays off. Once RustDesk listens, Listen
// succeeds and Healthy is true; if RustDesk stops listening, Healthy turns
// false though the switch is still on.
func TestListenFollowsDirectIPPort(t *testing.T) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DirectPort))
	if err != nil {
		t.Skipf("port %d is in use on this machine: %v", DirectPort, err)
	}
	ln.Close() // RustDesk with direct IP off: nothing listens

	c := New(nil, admission.Dialing{})
	err = c.Listen()
	if err == nil || !strings.Contains(err.Error(), "Enable direct IP access") {
		t.Fatalf("Listen with direct IP off = %v; want the prompt to enable it", err)
	}
	if c.ServerEnabled() || c.Healthy() {
		t.Fatal("server side on or healthy after a failed Listen")
	}

	ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DirectPort))
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
	if err := c.Listen(); err != nil {
		t.Fatalf("Listen with direct IP on: %v", err)
	}
	if !c.ServerEnabled() || !c.Healthy() {
		t.Fatal("server side not on and healthy after Listen")
	}

	ln.Close() // the user turns direct IP off again
	if !c.ServerEnabled() {
		t.Fatal("the switch changed by itself")
	}
	if c.Healthy() {
		t.Fatal("Healthy after RustDesk stopped listening")
	}
}
