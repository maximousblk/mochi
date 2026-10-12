package rustdesk

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// directIPKey is RustDesk's option for Settings → Security → "Enable direct IP
// access". RustDesk reads it on each pass of its direct-IP listener, and "Y"
// turns the listener on.
const directIPKey = "direct-server"

// Listen turns RustDesk's server side on. It first sets direct IP access in
// this user's RustDesk settings, the switch the forward needs; if that fails,
// the server side stays off. Other settings in the file are kept as they are.
func (c *Connector) Listen() error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := enableDirectIP(path); err != nil {
		return err
	}
	return c.ServerSwitch.Listen()
}

// settingsPath is where RustDesk keeps this user's settings on this OS.
func settingsPath() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding RustDesk settings: %w", err)
		}
		return filepath.Join(home, "Library", "Preferences", "com.carriez.RustDesk", "RustDesk2.toml"), nil
	case "windows":
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("finding RustDesk settings: %w", err)
		}
		return filepath.Join(dir, "RustDesk", "config", "RustDesk2.toml"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding RustDesk settings: %w", err)
	}
	return filepath.Join(dir, "rustdesk", "RustDesk2.toml"), nil
}

// enableDirectIP sets direct-server to "Y" under [options] in the RustDesk
// settings file at path, creating the file if RustDesk has never written one.
func enableDirectIP(path string) error {
	cfg := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if _, err := toml.Decode(string(raw), &cfg); err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("reading %s: %w", path, err)
	}
	opts, _ := cfg["options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	if v, _ := opts[directIPKey].(string); v == "Y" {
		return nil
	}
	opts[directIPKey] = "Y"
	cfg["options"] = opts

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write beside the file and rename it over, so RustDesk never reads half of it.
	tmp := path + ".mochi-tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
