package state

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"tailscale.com/types/key"
)

// Identity is a machine's one secret: an Ed25519 seed. Its public half is
// the machine's identity: what another machine lists in its bootstrap.json
// to trust this one's approvals. The seed signs the certificates behind
// "approve", and derives the tailcat node keys the machine serves and
// dials with.
type Identity struct{ priv ed25519.PrivateKey }

// NewIdentity generates a machine identity.
func NewIdentity() Identity {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return Identity{priv}
}

// IdentityFromSeed restores a machine identity from Seed.
func IdentityFromSeed(seed []byte) Identity { return Identity{ed25519.NewKeyFromSeed(seed)} }

// Seed is the secret to store.
func (i Identity) Seed() []byte { return i.priv.Seed() }

// Private is the signing key, for Issue and NewGate.
func (i Identity) Private() ed25519.PrivateKey { return i.priv }

// Public is the machine identity to share.
func (i Identity) Public() ed25519.PublicKey { return i.priv.Public().(ed25519.PublicKey) }

// Node key roles. A machine serves with one node key and dials with
// another. Two tailcat instances on one node key hold two relay
// connections under that key, and the relay delivers a packet to only one
// of them.
const (
	RoleServer = "server"
	RoleClient = "client"
)

// NodeKey derives the X25519 tailcat node key for role from the seed.
func (i Identity) NodeKey(role string) key.NodePrivate {
	x, err := hkdf.Key(sha256.New, i.priv.Seed(), nil, "mochi node key v1 "+role, 32)
	if err != nil {
		panic(err) // fixed-size output from SHA-256: cannot fail
	}
	x[0] &= 248
	x[31] &= 127
	x[31] |= 64
	var k key.NodePrivate
	if err := k.UnmarshalText([]byte("privkey:" + hex.EncodeToString(x))); err != nil {
		panic(err) // 32 hex-encoded bytes: cannot fail
	}
	return k
}
