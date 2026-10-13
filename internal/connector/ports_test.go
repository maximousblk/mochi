package connector

import (
	"net"
	"strings"
	"testing"
)

// A port is served from Claim until Unregister, and is refused otherwise.
func TestPortsServeOnlyWhileClaimed(t *testing.T) {
	p := NewPorts()
	if p.Registered(9) || p.Handler(9) != nil {
		t.Fatal("a port nothing claimed is served")
	}
	if err := p.Claim(9, func(net.Conn) {}); err != nil {
		t.Fatal(err)
	}
	if !p.Registered(9) || p.Handler(9) == nil {
		t.Fatal("a claimed port is not served")
	}
	p.Unregister(9)
	if p.Registered(9) || p.Handler(9) != nil {
		t.Fatal("a port is served after Unregister")
	}
	p.Unregister(9) // unregistering twice is fine
}

// Claim refuses a port another launcher holds, names the port in the error, and
// leaves the first launcher's handler in place.
func TestClaimRefusesHeldPort(t *testing.T) {
	p := NewPorts()
	first := func(net.Conn) {}
	if err := p.Claim(9, first); err != nil {
		t.Fatal(err)
	}
	err := p.Claim(9, func(net.Conn) {})
	if err == nil || !strings.Contains(err.Error(), "9") {
		t.Fatalf("second claim error = %v; want an error naming port 9", err)
	}
	if p.Handler(9) == nil {
		t.Fatal("the refused claim removed the first handler")
	}
}
