package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tailscale.com/types/key"
)

// A certificate issued with --key verifies against that key's public half, in
// either of the two key formats, and against no other.
func TestIssueWithKeyFile(t *testing.T) {
	ca := NewIdentity()
	other := NewIdentity()
	node := key.NewNode().Public()
	dir := t.TempDir()
	forms := map[string]string{
		"seed":     hex.EncodeToString(ca.Seed()),
		"full key": hex.EncodeToString(ca.Private()),
	}
	for name, content := range forms {
		keyFile := filepath.Join(dir, name+".key")
		out := filepath.Join(dir, name+".cert")
		if err := os.WriteFile(keyFile, []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runIssue([]string{"--node", node.String(), "--id", "alice", "--ttl", "1h", "--key", keyFile, "--out", out}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := hex.DecodeString(string(text[:len(text)-1]))
		if err != nil {
			t.Fatal(err)
		}
		if r, err := Verify(cert, []ed25519.PublicKey{ca.Public()}, node, time.Now()); err != nil || r.ID != "alice" {
			t.Fatalf("%s: Verify = %+v, %v", name, r, err)
		}
		if _, err := Verify(cert, []ed25519.PublicKey{other.Public()}, node, time.Now()); err == nil {
			t.Fatalf("%s: certificate verified against a different CA", name)
		}
	}
	bad := filepath.Join(dir, "bad.key")
	os.WriteFile(bad, []byte("abcd"), 0o600)
	if err := runIssue([]string{"--node", node.String(), "--id", "x", "--key", bad, "--out", filepath.Join(dir, "x")}); err == nil {
		t.Fatal("a 2-byte key was accepted")
	}
}
