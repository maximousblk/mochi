package rustdesk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/testharness"
)

// Turning on direct IP access keeps RustDesk's other settings and sets the
// option to "Y".
func TestEnableDirectIPKeepsOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "RustDesk2.toml")
	existing := "[options]\nlocal-ip-addr = '10.0.0.5'\ndirect-server = 'N'\n\n[ui_flutter]\nwindow-size = '800'\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := enableDirectIP(path); err != nil {
		t.Fatalf("enableDirectIP: %v", err)
	}

	var got struct {
		Options   map[string]string `toml:"options"`
		UIFlutter map[string]string `toml:"ui_flutter"`
	}
	if _, err := toml.DecodeFile(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Options[directIPKey] != "Y" {
		t.Errorf("direct-server = %q; want \"Y\"", got.Options[directIPKey])
	}
	if got.Options["local-ip-addr"] != "10.0.0.5" {
		t.Errorf("local-ip-addr = %q; want it kept", got.Options["local-ip-addr"])
	}
	if got.UIFlutter["window-size"] != "800" {
		t.Errorf("ui_flutter window-size = %q; want it kept", got.UIFlutter["window-size"])
	}
}

// With no settings file yet, enabling direct IP creates one.
func TestEnableDirectIPCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "RustDesk", "config", "RustDesk2.toml")
	if err := enableDirectIP(path); err != nil {
		t.Fatalf("enableDirectIP: %v", err)
	}
	var got struct {
		Options map[string]string `toml:"options"`
	}
	if _, err := toml.DecodeFile(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Options[directIPKey] != "Y" {
		t.Errorf("direct-server = %q; want \"Y\"", got.Options[directIPKey])
	}
}

// Listen turns the server side on and sets direct IP access in the user's
// RustDesk settings.
func TestListenEnablesDirectIPAndServes(t *testing.T) {
	testharness.IsolateUserDirs(t)
	c := New(nil, admission.Dialing{})

	if err := c.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if !c.ServerEnabled() {
		t.Fatal("server side off after a successful Listen")
	}
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Options map[string]string `toml:"options"`
	}
	if _, err := toml.DecodeFile(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Options[directIPKey] != "Y" {
		t.Errorf("direct-server = %q; want \"Y\"", got.Options[directIPKey])
	}
}

// If the settings cannot be written, Listen fails and the server side stays
// off, so the switch never shows on while RustDesk refuses direct IP.
func TestListenFailureLeavesServerOff(t *testing.T) {
	testharness.IsolateUserDirs(t)
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file where RustDesk's settings directory should be.
	if err := os.WriteFile(filepath.Dir(path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(nil, admission.Dialing{})

	if err := c.Listen(); err == nil {
		t.Fatal("Listen succeeded though the settings could not be written")
	}
	if c.ServerEnabled() {
		t.Fatal("server side on after a failed Listen")
	}
}
