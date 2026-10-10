package textsender

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/testharness"
	"tailscale.com/types/key"
)

// Send delivers the text to the port on the host, then closes the connection
// so the receiver sees end of input.
func TestSendDeliversTextOverTailcat(t *testing.T) {
	relay := testharness.Relay(t)

	const port uint16 = 9000
	received := make(chan string, 1)
	host := testharness.Host(t, relay, map[uint16]func(net.Conn){
		port: func(conn net.Conn) {
			defer conn.Close()
			b, _ := io.ReadAll(conn)
			received <- string(b)
		},
	})

	const text = "hello from mochi\n"
	n, err := Send(context.Background(), admission.Dialing{Key: key.NewNode()}, testharness.Address(host), port, text)
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
