package rustdesk

import (
	"fmt"
	"net"
	"time"
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
//     actually does. Only the default port, 21118, is probed.
//
// So Mochi asks the user to turn the option on and checks the result.

// probe reports whether something accepts connections on RustDesk's default
// direct-IP port on this machine.
func probe() error {
	addr := fmt.Sprintf("127.0.0.1:%d", DirectPort)
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return fmt.Errorf("RustDesk is not accepting direct IP connections on port %d: in RustDesk, open Settings > Security, turn on \"Enable direct IP access\", then try again: %w", DirectPort, err)
	}
	conn.Close()
	return nil
}

// Listen turns the server side on, if RustDesk accepts direct IP connections
// on this machine. If it does not, Listen fails and the server side stays off.
func (c *Connector) Listen() error {
	if err := probe(); err != nil {
		return err
	}
	return c.ServerSwitch.Listen()
}

// Healthy reports whether the server side is on and RustDesk still accepts
// direct IP connections, so it is false if the user turned the option off after
// Listen.
func (c *Connector) Healthy() bool {
	return c.ServerEnabled() && probe() == nil
}
