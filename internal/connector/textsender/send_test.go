package textsender

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/tailscale/tailcat"
	"tailscale.com/tstest/integration"
	"tailscale.com/types/key"
)

func quiet(string, ...any) {}

// Send delivers the text to the port on the host, then closes the connection
// so the receiver sees end of input.
func TestSendDeliversTextOverTailcat(t *testing.T) {
	t.Setenv("TS_DEBUG_ALWAYS_USE_DERP", "1")
	dm := integration.RunDERPAndSTUN(t, quiet, "127.0.0.1")

	const port uint16 = 9000
	received := make(chan string, 1)
	s := &tailcat.Server{Region: dm.Regions[1], Logf: quiet}
	s.OnTCP = func(p uint16) func(net.Conn) {
		if p != port {
			return nil
		}
		return func(conn net.Conn) {
			defer conn.Close()
			b, _ := io.ReadAll(conn)
			received <- string(b)
		}
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	const text = "hello from mochi\n"
	n, err := Send(context.Background(), admission.Dialing{Key: key.NewNode()}, string(s.TailcatAddr()), port, text)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if n != len(text) {
		t.Fatalf("Send wrote %d bytes, want %d", n, len(text))
	}
	if got := <-received; got != text {
		t.Fatalf("receiver got %q, want %q", got, text)
	}
}

func TestSendRejectsMalformedAddressBeforeDialing(t *testing.T) {
	_, err := Send(context.Background(), admission.Dialing{}, "not-a-tailcat-address", 1, "x")
	if err == nil {
		t.Fatal("Send accepted a malformed address")
	}
}
