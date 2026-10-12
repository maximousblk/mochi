// Package testharness starts the local Tailcat network that feature tests run
// on: a DERP relay and Tailcat hosts that serve the ports a test needs. Tests
// use it instead of setting up tailcat themselves. It must not import Mochi's
// own packages, so that their tests can use it without an import cycle.
package testharness

import (
	"net"
	"testing"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/tstest/integration"
)

// Quiet discards tailcat's logging.
func Quiet(string, ...any) {}

// Relay starts a local DERP relay and returns its region. Traffic always goes
// through the relay, so tests do not depend on direct paths between sockets.
func Relay(t testing.TB) *tailcfg.DERPRegion {
	t.Helper()
	t.Setenv("TS_DEBUG_ALWAYS_USE_DERP", "1")
	return integration.RunDERPAndSTUN(t, Quiet, "127.0.0.1").Regions[1]
}

// Host starts a Tailcat server on relay that serves the ports in serve. A port
// missing from serve refuses connections, as the tailcat CLI does for ports it
// does not serve. The server is closed when the test ends.
func Host(t testing.TB, relay *tailcfg.DERPRegion, serve map[uint16]func(net.Conn)) *tailcat.Server {
	t.Helper()
	return HostFunc(t, relay, func(port uint16) func(net.Conn) { return serve[port] })
}

// HostFunc is Host with the handler for each port looked up as each connection
// arrives, so a test can change what the server serves while it runs.
func HostFunc(t testing.TB, relay *tailcfg.DERPRegion, serve func(port uint16) func(net.Conn)) *tailcat.Server {
	t.Helper()
	s := &tailcat.Server{Region: relay, Logf: Quiet}
	s.OnTCP = serve
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Address is the Tailcat address a client dials to reach s.
func Address(s *tailcat.Server) string { return string(s.TailcatAddr()) }
