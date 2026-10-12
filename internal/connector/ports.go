package connector

import (
	"net"
	"sync"
)

// Ports is the set of Tailcat ports this machine serves in process. The app's
// Tailcat server looks up each connection's port here as the connection
// arrives, so a launcher serves its port from Listen until Stop, with no restart.
type Ports struct {
	mu       sync.Mutex
	handlers map[uint16]func(net.Conn)
}

// NewPorts returns a set with no ports served.
func NewPorts() *Ports {
	return &Ports{handlers: map[uint16]func(net.Conn){}}
}

// Register serves port with handler, replacing the handler it had.
func (p *Ports) Register(port uint16, handler func(net.Conn)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[port] = handler
}

// Unregister stops serving port. Connections already being handled continue.
func (p *Ports) Unregister(port uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.handlers, port)
}

// Handler returns the handler for port, or nil if the port is not served.
func (p *Ports) Handler(port uint16) func(net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.handlers[port]
}

// Registered reports whether port is served.
func (p *Ports) Registered(port uint16) bool {
	return p.Handler(port) != nil
}
