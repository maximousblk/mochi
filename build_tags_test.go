package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// build-tags.txt, which the flake builds Mochi with, must match the list the
// pinned Tailcat ships. A stale copy after a Tailcat bump could leave out a
// feature Tailcat has since come to need, such as nattraversal for direct paths.
func TestBuildTagsMatchTailcat(t *testing.T) {
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/tailscale/tailcat").Output()
	if err != nil {
		t.Fatalf("locate the tailcat module: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(string(bytes.TrimSpace(dir)), "build-tags.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("build-tags.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatal("build-tags.txt differs from the pinned tailcat's; copy it from the tailcat module")
	}
}
