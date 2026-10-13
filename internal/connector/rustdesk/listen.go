package rustdesk

import (
	"fmt"
	"net"
	"time"

	"github.com/ashwin-y/mochi/internal/connector"
)

// probeTimeout bounds one probe of the local direct-IP port. It is a loopback
// connection, so it answers at once or not at all.
const probeTimeout = 2 * time.Second

// Whether direct IP access is on is decided by probing, never by reading or
// writing RustDesk's settings file. This is deliberate and the reliable way:
//
//   - RustDesk loads RustDesk2.toml once at start and keeps it in memory, so
//     the file can be stale, and any setting RustDesk saves later rewrites it
//     from memory, undoing an edit made behind its back.
//   - On Windows the installed service and the user's GUI can use different
//     profiles, so the file Mochi sees may not be the one that is in effect.
//   - The listening port is the one thing that shows what the running RustDesk
//     actually does. Mochi probes the RustDesk port its server was started with.
//
// So Mochi asks the user to turn the option on and checks the result.
// Listen does not probe: the server side can be turned on first, and Healthy
// reports whether RustDesk is accepting yet.

// Listen serves the configured Tailcat port, forwarding each connection to
// RustDesk's direct-IP port on this machine. Direct IP access must be on in
// RustDesk's own settings for the forward to work. It fails if another launcher
// already serves that Tailcat port.
func (c *Connector) Listen(cfg connector.Config) error {
	port, err := cfg.Port("port")
	if err != nil {
		return err
	}
	localPort, err := cfg.Port("local_port")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ports.Claim(port, forwardTo(fmt.Sprintf("127.0.0.1:%d", localPort))); err != nil {
		return err
	}
	c.serving = &listening{port: port, localPort: localPort}
	return nil
}

// Stop stops serving the direct-IP port.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.serving != nil {
		c.ports.Unregister(c.serving.port)
		c.serving = nil
	}
	return nil
}

// Healthy reports whether the server side is on and RustDesk accepts direct IP
// connections on the port it was listening with. It is false while the user has
// not turned "Enable direct IP access" on in RustDesk (Settings > Security), or
// has turned it off again. It dials, so callers poll it off the UI thread.
func (c *Connector) Healthy() bool {
	c.mu.Lock()
	s := c.serving
	c.mu.Unlock()
	return s != nil && c.ports.Registered(s.port) && probe(s.localPort) == nil
}

// probe reports whether something accepts connections on RustDesk's direct-IP
// port on this machine.
func probe(port uint16) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// forwardTo returns a handler that pipes each connection to the local TCP
// address addr, and closes both sides when either one ends.
func forwardTo(addr string) func(net.Conn) {
	return func(remote net.Conn) {
		local, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			remote.Close()
			return
		}
		connector.Splice(remote, local, nil)
	}
}
