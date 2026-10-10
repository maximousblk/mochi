package admission

import (
	"crypto/ed25519"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/testharness"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func dialer(e *env, k key.NodePrivate, certs ...[]byte) *Dialer {
	d := NewDialer(e.addr, k, func() [][]byte { return certs }, testharness.Quiet)
	e.t.Cleanup(func() { d.Close() })
	return d
}

func dialEcho(d *Dialer) error {
	conn, err := d.DialTCPPort(ctx(20*time.Second), echoPort)
	if err != nil {
		return err
	}
	defer conn.Close()
	return echo(conn)
}

func refusal(t *testing.T, err error) string {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) {
		t.Fatalf("err = %v; want a *Refused", err)
	}
	return r.Reason
}

func TestDialer(t *testing.T) {
	ext, extPriv := newCA(t)
	_, evil := newCA(t)
	e := newEnv(t, nil, ext)

	t.Run("not_approved_says_why_and_the_admin_sees_it", func(t *testing.T) {
		k := key.NewNode()
		d := dialer(e, k)
		if got := refusal(t, dialEcho(d)); got != "pending approval" {
			t.Fatalf("reason = %q", got)
		}
		if r := e.row(k.Public()); r.State != Probation || r.Reason != "no certificate" {
			t.Fatalf("row = %+v", r)
		}
		// The admin approves it; the same Dialer gets in with no certificate.
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		if err := dialEcho(d); err != nil {
			t.Fatalf("dial after Approve: %v", err)
		}
	})

	t.Run("a_certificate_from_a_trusted_ca_approves_it", func(t *testing.T) {
		k := key.NewNode()
		d := dialer(e, k, issue(t, extPriv, k.Public(), time.Hour))
		if err := dialEcho(d); err != nil {
			t.Fatal(err)
		}
		d.Close()
		// Later connections need no certificate at all.
		if err := dialEcho(dialer(e, k)); err != nil {
			t.Fatalf("reconnect without a certificate: %v", err)
		}
	})

	t.Run("an_approved_node_may_carry_certificates_this_host_does_not_trust", func(t *testing.T) {
		k := key.NewNode()
		if err := e.gate.Approve(k.Public()); err != nil {
			t.Fatal(err)
		}
		d := dialer(e, k, issue(t, evil, k.Public(), time.Hour), []byte("not even a certificate"))
		if err := dialEcho(d); err != nil {
			t.Fatalf("an approved node was stopped by a certificate this host does not accept: %v", err)
		}
	})

	t.Run("the_second_certificate_is_tried_when_the_first_is_refused", func(t *testing.T) {
		k := key.NewNode()
		d := dialer(e, k, issue(t, evil, k.Public(), time.Hour), issue(t, extPriv, k.Public(), time.Hour))
		if err := dialEcho(d); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a_refused_certificate_says_why", func(t *testing.T) {
		k := key.NewNode()
		d := dialer(e, k, issue(t, evil, k.Public(), time.Hour))
		got := refusal(t, dialEcho(d))
		if !strings.Contains(got, "signed by a CA the host trusts") {
			t.Fatalf("reason = %q", got)
		}
		// The reason names the CAs the host trusts: its own and the external one.
		for _, ca := range []ed25519.PublicKey{ext, e.gate.self.Public().(ed25519.PublicKey)} {
			if !strings.Contains(got, Fingerprint(ca)) {
				t.Fatalf("reason = %q; missing trusted CA %s", got, Fingerprint(ca))
			}
		}
		k2 := key.NewNode()
		d2 := dialer(e, k2, issue(t, extPriv, k2.Public(), -time.Second))
		if got := refusal(t, dialEcho(d2)); got != "expired" {
			t.Fatalf("reason = %q", got)
		}
	})

	// A blocked node is not admitted at all: it gets no tunnel, so it cannot
	// even reach the proof port to be told. Like a stranger refused by
	// --allow, it hears nothing, and its dial times out.
	t.Run("blocked_hears_nothing", func(t *testing.T) {
		k := key.NewNode()
		cert := issue(t, extPriv, k.Public(), time.Hour)
		e.gate.Block(k.Public())
		d := dialer(e, k, cert)
		_, err := d.DialTCPPort(ctx(5*time.Second), echoPort)
		var r *Refused
		if err == nil || errors.As(err, &r) {
			t.Fatalf("err = %v; want a timeout, since a blocked node gets no tunnel", err)
		}
		if row := e.row(k.Public()); row.State != Blocked {
			t.Fatalf("row = %+v; the certificate got a blocked node approved", row)
		}
	})

	t.Run("too_many_certificates_are_refused", func(t *testing.T) {
		k := key.NewNode()
		var certs [][]byte
		for range maxCerts + 1 {
			certs = append(certs, issue(t, evil, k.Public(), time.Hour))
		}
		if got := refusal(t, dialEcho(dialer(e, k, certs...))); got != "bad request" {
			t.Fatalf("reason = %q", got)
		}
	})
}

// A server that is not a Mochi host has nothing on the proof port. The Dialer
// must not get in the way: the dial goes through, and costs about one extra
// round trip.
func TestDialerAgainstAPlainTailcatServer(t *testing.T) {
	relay := testharness.Relay(t)
	s := &tailcat.Server{Region: relay, Logf: testharness.Quiet}
	s.OnTCP = func(port uint16) func(net.Conn) {
		if port != echoPort {
			return nil // a refused connection, as the tailcat CLI does for ports it does not serve
		}
		return func(c net.Conn) { defer c.Close(); io := make([]byte, 1); c.Read(io); c.Write(io) }
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	timed := func(dial func() (net.Conn, error)) time.Duration {
		start := time.Now()
		conn, err := dial()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := echo(conn); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}

	bare := &tailcat.Client{Server: s.TailcatAddr(), Key: key.NewNode(), Logf: testharness.Quiet}
	d := NewDialer(s.TailcatAddr(), key.NewNode(), nil, testharness.Quiet)
	t.Cleanup(func() { bare.Close(); d.Close() })
	bareDial := func() (net.Conn, error) { return bare.DialTCPPort(ctx(20*time.Second), echoPort) }
	dialerDial := func() (net.Conn, error) { return d.DialTCPPort(ctx(20*time.Second), echoPort) }

	coldBare, coldDialer := timed(bareDial), timed(dialerDial)
	var warmBare, warmDialer time.Duration
	for range 5 {
		warmBare += timed(bareDial)
		warmDialer += timed(dialerDial)
	}
	warmBare, warmDialer = warmBare/5, warmDialer/5
	t.Logf("poc: plain tailcat server, first dial: bare %v, Dialer %v; warm dial (mean of 5): bare %v, Dialer %v",
		coldBare.Round(time.Millisecond), coldDialer.Round(time.Millisecond),
		warmBare.Round(time.Millisecond), warmDialer.Round(time.Millisecond))
	if extra := warmDialer - warmBare; extra > time.Second {
		t.Fatalf("the proof attempt adds %v to a warm dial against a server with no proof port", extra)
	}
}
