// Node certificates admit tailcat clients that hold a certificate from a
// trusted CA, using only the stock Server.AllowClient hook.
package admission

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/fxamacker/cbor/v2"
	"tailscale.com/types/key"
)

// sigContext separates these signatures from anything else a CA key signs.
const sigContext = "mochi node cert v1\x00"

// Cert is a CA's statement that a node key may connect until Exp.
// Claims holds the CBOR-encoded claims exactly as signed, so the
// verifier checks the bytes it then decodes.
type Cert struct {
	Claims []byte `cbor:"1,keyasint"`
	Sig    []byte `cbor:"2,keyasint"` // Ed25519 over sigContext || Claims
}

type claims struct {
	Node []byte `cbor:"1,keyasint"` // tailcat node public key, raw 32 bytes
	ID   string `cbor:"2,keyasint"` // who it was issued to, for logs and UI
	Exp  int64  `cbor:"3,keyasint"` // Unix seconds
}

var (
	enc, _ = cbor.CTAP2EncOptions().EncMode()
	dec, _ = cbor.DecOptions{
		DupMapKey:         cbor.DupMapKeyEnforcedAPF,
		ExtraReturnErrors: cbor.ExtraDecErrorUnknownField,
	}.DecMode()
)

// Issue signs a certificate for node with the CA key ca.
func Issue(ca ed25519.PrivateKey, node key.NodePublic, id string, exp time.Time) ([]byte, error) {
	raw := node.Raw32()
	c, err := enc.Marshal(claims{Node: raw[:], ID: id, Exp: exp.Unix()})
	if err != nil {
		return nil, err
	}
	return enc.Marshal(Cert{Claims: c, Sig: ed25519.Sign(ca, append([]byte(sigContext), c...))})
}

// errNotTrusted is Verify's error when none of the given CAs signed the certificate.
var errNotTrusted = errors.New("not signed by a trusted CA")

// Result is what a valid certificate says.
type Result struct {
	ID  string
	Exp time.Time
	CA  ed25519.PublicKey // which of the trusted CAs signed it
}

// Verify checks that one of cas signed cert for node and that it has not
// expired at now.
func Verify(cert []byte, cas []ed25519.PublicKey, node key.NodePublic, now time.Time) (Result, error) {
	var ct Cert
	if err := dec.Unmarshal(cert, &ct); err != nil {
		return Result{}, err
	}
	msg := append([]byte(sigContext), ct.Claims...)
	var signer ed25519.PublicKey
	for _, ca := range cas {
		if ed25519.Verify(ca, msg, ct.Sig) {
			signer = ca
			break
		}
	}
	if signer == nil {
		return Result{}, errNotTrusted
	}
	var c claims
	if err := dec.Unmarshal(ct.Claims, &c); err != nil {
		return Result{}, err
	}
	raw := node.Raw32()
	if !bytes.Equal(c.Node, raw[:]) {
		return Result{}, errors.New("issued to a different node key")
	}
	exp := time.Unix(c.Exp, 0)
	if !now.Before(exp) {
		return Result{}, errors.New("expired")
	}
	return Result{ID: c.ID, Exp: exp, CA: signer}, nil
}

// Fingerprint names a CA key compactly, for storage and display.
func Fingerprint(ca ed25519.PublicKey) string {
	h := sha256.Sum256(ca)
	return hex.EncodeToString(h[:8])
}
