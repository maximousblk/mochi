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
// Listen does not probe: the server side can be turned on first, and Healthy
// reports whether RustDesk is accepting yet.

// probe reports whether something accepts connections on RustDesk's default
// direct-IP port on this machine.
func probe() error {
	addr := fmt.Sprintf("127.0.0.1:%d", DirectPort)
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// Healthy reports whether the server side is on and RustDesk accepts direct IP
// connections on this machine. It is false while the user has not turned
// "Enable direct IP access" on in RustDesk (Settings > Security), or has turned
// it off again. It dials, so callers poll it off the UI thread.
func (c *Connector) Healthy() bool {
	return c.ServerEnabled() && probe() == nil
}
