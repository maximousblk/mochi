package textsender

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/testharness"
	"tailscale.com/types/key"
)

// A text sent to DefaultPort on a Tailcat host arrives exactly once through
// the receiver's callback.
func TestReceiverDeliversSentText(t *testing.T) {
	relay := testharness.Relay(t)
	ports := connector.NewPorts()
	got := make(chan Text, 1)
	r := NewReceiver(ports, nil, func() bool { return true }, func(text Text) { got <- text })
	if err := r.Listen(connector.Config{"port": fmt.Sprint(DefaultPort)}); err != nil {
		t.Fatal(err)
	}
	host := testharness.HostFunc(t, relay, ports.Handler)

	const body = "hello from mochi\n"
	if _, err := Send(context.Background(), admission.Dialing{Key: key.NewNode()}, testharness.Address(host), DefaultPort, body); err != nil {
		t.Fatalf("send: %v", err)
	}
	text := <-got
	if text.Body != body || text.Truncated {
		t.Fatalf("received %+v, want body %q and not truncated", text, body)
	}
}

// A text longer than MaxBytes keeps only its first MaxBytes and is marked
// Truncated, so one sender cannot fill the inbox.
func TestReceiverTruncatesLongText(t *testing.T) {
	relay := testharness.Relay(t)
	ports := connector.NewPorts()
	got := make(chan Text, 1)
	r := NewReceiver(ports, nil, func() bool { return true }, func(text Text) { got <- text })
	if err := r.Listen(connector.Config{"port": fmt.Sprint(DefaultPort)}); err != nil {
		t.Fatal(err)
	}
	host := testharness.HostFunc(t, relay, ports.Handler)

	long := bytes.Repeat([]byte("a"), MaxBytes+100)
	if _, err := Send(context.Background(), admission.Dialing{Key: key.NewNode()}, testharness.Address(host), DefaultPort, string(long)); err != nil {
		t.Fatalf("send: %v", err)
	}
	text := <-got
	if len(text.Body) != MaxBytes || !text.Truncated {
		t.Fatalf("received %d bytes truncated=%v, want %d bytes truncated", len(text.Body), text.Truncated, MaxBytes)
	}
	if strings.Trim(text.Body, "a") != "" {
		t.Fatal("the kept bytes are not the start of the text")
	}
}

// The receiver is healthy only while it listens and the app can notify.
func TestReceiverHealthyOnlyWhileListeningAndNotifying(t *testing.T) {
	ports := connector.NewPorts()
	canNotify := true
	r := NewReceiver(ports, nil, func() bool { return canNotify }, func(Text) {})

	if r.Healthy() {
		t.Fatal("healthy before Listen")
	}
	if err := r.Listen(connector.Config{"port": fmt.Sprint(DefaultPort)}); err != nil {
		t.Fatal(err)
	}
	if !r.Healthy() {
		t.Fatal("not healthy while listening and able to notify")
	}
	canNotify = false
	if r.Healthy() {
		t.Fatal("healthy without notifications, so a text would go unseen")
	}
	canNotify = true
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if r.Healthy() {
		t.Fatal("healthy after Stop")
	}
}
