package connector

import (
	"net"
	"testing"
)

// A port is served from Register until Unregister, and is refused otherwise.
func TestPortsServeOnlyWhileRegistered(t *testing.T) {
	p := NewPorts()
	if p.Registered(9) || p.Handler(9) != nil {
		t.Fatal("a port nothing registered is served")
	}
	p.Register(9, func(net.Conn) {})
	if !p.Registered(9) || p.Handler(9) == nil {
		t.Fatal("a registered port is not served")
	}
	p.Unregister(9)
	if p.Registered(9) || p.Handler(9) != nil {
		t.Fatal("a port is served after Unregister")
	}
	p.Unregister(9) // unregistering twice is fine
}
