package admission

import (
	"context"
	"errors"
	"net"
	"strings"
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
}

// Dialing is what a connector needs to reach any host: this machine's client
// node key and the certificates it presents. The zero value dials with no key.
type Dialing struct {
	Key   key.NodePrivate
	Certs func() [][]byte
}

// Dialer returns a Dialer for the server at addr.
func (d Dialing) Dialer(addr tailcat.Addr) *Dialer { return NewDialer(addr, d.Key, d.Certs, nil) }

// NewDialer returns a Dialer for the server at addr, dialing with k.
func NewDialer(addr tailcat.Addr, k key.NodePrivate, certs func() [][]byte, logf func(string, ...any)) *Dialer {
	return &Dialer{Client: &tailcat.Client{Server: addr, Key: k, Logf: logf}, Certs: certs}
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
