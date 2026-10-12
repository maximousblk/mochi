package admission

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// Dialer is a tailcat client that introduces this machine to Mochi hosts.
// Before every service dial it asks the host whether it approves this machine,
// presenting the certificates Certs returns. A host that says no stops the
// dial with a *Refused, so the caller can say why instead of seeing a
// connection reset. A server that is not a Mochi host has nothing on the proof
// port; the dial then goes ahead as it would have without this wrapper.
type Dialer struct {
	*tailcat.Client
	// Certs returns the certificates to present, if any. It may be nil.
	Certs func() [][]byte

	release func() error // gives back a shared client; nil if Client is the Dialer's own
	once    sync.Once
}

// Dialing is what a connector needs to reach any host: this machine's client
// node key and the certificates it presents. The zero value dials with no key.
type Dialing struct {
	Key   key.NodePrivate
	Certs func() [][]byte
}

// Dialer returns a Dialer for the server at addr. Dialers for the same server
// and key share one tailcat client, which closes with the last of them: a
// server sees one peer per node key, so two clients with the same key would
// take the WireGuard session from each other, and the connections of the one
// that lost it would stall. With no key, each Dialer has a client of its own.
func (d Dialing) Dialer(addr tailcat.Addr) *Dialer {
	if d.Key.IsZero() {
		return NewDialer(addr, d.Key, d.Certs, nil)
	}
	id := sharedID{node: d.Key.Public(), server: addr}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	c := shared.clients[id]
	if c == nil {
		c = &sharedClient{client: &tailcat.Client{Server: addr, Key: d.Key}}
		shared.clients[id] = c
	}
	c.refs++
	return &Dialer{Client: c.client, Certs: d.Certs, release: func() error {
		shared.mu.Lock()
		defer shared.mu.Unlock()
		if c.refs--; c.refs > 0 {
			return nil
		}
		delete(shared.clients, id)
		// Closed under the lock, so no new client for this key starts while
		// this one still holds the session.
		return c.client.Close()
	}}
}

// shared holds the tailcat clients Dialing's Dialers share.
var shared = struct {
	mu      sync.Mutex
	clients map[sharedID]*sharedClient
}{clients: map[sharedID]*sharedClient{}}

type sharedID struct {
	node   key.NodePublic
	server tailcat.Addr
}

type sharedClient struct {
	client *tailcat.Client
	refs   int // open Dialers using client
}

// NewDialer returns a Dialer for the server at addr, dialing with k through a
// client of its own.
func NewDialer(addr tailcat.Addr, k key.NodePrivate, certs func() [][]byte, logf func(string, ...any)) *Dialer {
	return &Dialer{Client: &tailcat.Client{Server: addr, Key: k, Logf: logf}, Certs: certs}
}

// Close closes the Dialer. A shared client stays open while other Dialers use
// it. Close may be repeated.
func (d *Dialer) Close() error {
	if d.release == nil {
		return d.Client.Close()
	}
	var err error
	d.once.Do(func() { err = d.release() })
	return err
}

// DialTCPPort replaces tailcat.Client's, so callers need no other change.
func (d *Dialer) DialTCPPort(ctx context.Context, port uint16) (net.Conn, error) {
	var certs [][]byte
	if d.Certs != nil {
		certs = d.Certs()
	}
	err := Prove(ctx, d.Client, ProofPort, certs)
	var refused *Refused
	if errors.As(err, &refused) {
		return nil, explainRefusal(refused, certs, d.Client.Key.Public())
	}
	if ctx.Err() != nil {
		return nil, err
	}
	return d.Client.DialTCPPort(ctx, port)
}

// explainRefusal checks the certificates this machine holds against the CAs
// the host listed. If it holds some and none was signed by any of them, no
// retry can help, and the error says which CAs the host would accept.
func explainRefusal(r *Refused, certs [][]byte, node key.NodePublic) error {
	if len(certs) == 0 || len(r.CAs) == 0 {
		return r
	}
	for _, cert := range certs {
		if _, err := Verify(cert, r.CAs, node, time.Now()); !errors.Is(err, errNotTrusted) {
			return r // signed by a CA the host trusts; the host's reason says what else is wrong
		}
	}
	var fps []string
	for _, ca := range r.CAs {
		fps = append(fps, Fingerprint(ca))
	}
	return &Refused{
		Reason: "none of this machine's certificates is signed by a CA the host trusts (it trusts " + strings.Join(fps, ", ") + ")",
		CAs:    r.CAs,
	}
}
