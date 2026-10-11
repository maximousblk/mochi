package testharness

import (
	"os"
	"path/filepath"
	"testing"
)

// FakeRustDesk installs a RustDesk.exe under a temporary Program Files, where
// the RustDesk launcher looks for it. The fake records the arguments it is
// started with, so the launch runs for real and a test can check it. It returns
// the file the arguments are written to, which holds one argument per line.
func FakeRustDesk(t testing.TB) string {
	t.Helper()
	programFiles := t.TempDir()
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", t.TempDir())
	dir := filepath.Join(programFiles, "RustDesk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(programFiles, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "RustDesk.exe"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return argsFile
}
