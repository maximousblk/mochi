// Package cmd holds Mochi's command-line subcommands. The root main dispatches
// to them; the GUI lives in package app.
package cmd

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/state"
	"tailscale.com/types/key"
)

// Issue implements `mochi issue`: sign a certificate for a client node key
// with a CA key: the one in --key, or this machine's own. A host that lists the
// CA's public key in its bootstrap.json trusts the certificate. The
// certificate goes to a file because Windows builds have no console to print to.
func Issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	node := fs.String("node", "", "the client's node key (nodekey:...), shown in its Settings")
	id := fs.String("id", "", "who the certificate is for")
	ttl := fs.Duration("ttl", 12*time.Hour, "how long the certificate is valid")
	out := fs.String("out", "", "file to write the certificate to")
	caKey := fs.String("key", "", "file holding the CA's private key as hex (a 32-byte seed or a 64-byte key); default: this machine's own key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *node == "" || *id == "" || *out == "" {
		return errors.New("usage: mochi issue --node nodekey:... --id name --ttl 12h --out file")
	}
	var k key.NodePublic
	if err := k.UnmarshalText([]byte(*node)); err != nil {
		return errors.New("invalid node key: " + err.Error())
	}
	var priv ed25519.PrivateKey
	if *caKey != "" {
		p, err := readCAKey(*caKey)
		if err != nil {
			return err
		}
		priv = p
	} else {
		path, err := state.Path()
		if err != nil {
			return err
		}
		legacy, err := state.LegacyPath()
		if err != nil {
			return err
		}
		store, err := state.OpenState(path, legacy)
		if err != nil {
			return err
		}
		defer store.Close()
		identity, err := store.Identity()
		if err != nil {
			return err
		}
		priv = identity.Private()
	}
	cert, err := admission.Issue(priv, k, *id, time.Now().Add(*ttl))
	if err != nil {
		return err
	}
	return os.WriteFile(*out, []byte(hex.EncodeToString(cert)+"\n"), 0o600)
}

// readCAKey reads a CA private key from a file holding it as hex: a 32-byte
// seed, which is what a machine stores, or a full 64-byte key.
func readCAKey(path string) (ed25519.PrivateKey, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(text)))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: the key is not valid hex", path)
	case len(raw) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case len(raw) == ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	}
	return nil, fmt.Errorf("%s: the key is %d bytes; want %d (a seed) or %d", path, len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
}
