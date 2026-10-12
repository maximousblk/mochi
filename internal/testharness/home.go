package testharness

import (
	"path/filepath"
	"testing"
)

// IsolateUserDirs points the user's home and config directories at temporary
// ones. Code under test that writes there, such as RustDesk's settings file,
// then never touches the real ones.
func IsolateUserDirs(t testing.TB) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
}
