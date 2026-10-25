package state

import (
	"os"
	"path/filepath"
)

// Dir returns Mochi's per-user directory for logs and PocketBase data.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mochi"), nil
}

// DataDir returns the PocketBase database directory.
func DataDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data"), nil
}
