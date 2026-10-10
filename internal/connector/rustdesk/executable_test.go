package rustdesk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher finds RustDesk.exe in the standard Program Files locations and
// says where it looked when it is not installed.
func TestExecutableFindsInstallInProgramFiles(t *testing.T) {
	programFiles := t.TempDir()
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", t.TempDir())

	dir := filepath.Join(programFiles, "RustDesk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "RustDesk.exe")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := executable()
	if err != nil || got != exe {
		t.Fatalf("executable() = %q, %v; want %q", got, err, exe)
	}
}

func TestExecutableFallsBackToSecondProgramFilesDirectory(t *testing.T) {
	t.Setenv("ProgramFiles", t.TempDir())
	x86 := t.TempDir()
	t.Setenv("ProgramFiles(x86)", x86)

	dir := filepath.Join(x86, "RustDesk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "RustDesk.exe")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := executable()
	if err != nil || got != exe {
		t.Fatalf("executable() = %q, %v; want %q", got, err, exe)
	}
}

func TestExecutableReportsMissingInstall(t *testing.T) {
	t.Setenv("ProgramFiles", t.TempDir())
	t.Setenv("ProgramFiles(x86)", t.TempDir())

	_, err := executable()
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("executable() error = %v; want a not-installed message", err)
	}
}
