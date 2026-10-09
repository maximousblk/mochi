package state

import (
	"os"
	"path/filepath"
)

// Path is the state database of this user: mochi.db in the user config directory.
func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mochi", "mochi.db"), nil
}

// LegacyPath is the JSON file older builds kept their hosts in. It is migrated
// into the database when present.
func LegacyPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mochi", "config.json"), nil
}
