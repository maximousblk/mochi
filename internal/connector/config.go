package connector

import (
	"fmt"
	"strconv"

	"github.com/ashwin-y/mochi/internal/admission"
)

// Field is one setting of a launcher.
type Field struct {
	Key     string             // saved; stable
	Label   string             // shown in the dialog
	Default string             // used while the user has not saved a value
	Help    string             // one line shown under the input; may be empty
	Check   func(string) error // nil accepts any value
}

// Config holds a launcher's settings by Field.Key.
type Config map[string]string

// Port returns the TCP port of a resolved config under key. A port that was
// checked by CheckPort always parses.
func (c Config) Port(key string) (uint16, error) {
	port, err := strconv.ParseUint(c[key], 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid TCP port %q", c[key])
	}
	return uint16(port), nil
}

// Resolve returns saved completed with defaults for missing or empty keys, without
// keys no field declares, after running every Check. The first failing Check wins,
// and its error names the field's Label, so the dialog can point at the input that
// is wrong.
func Resolve(fields []Field, saved Config) (Config, error) {
	resolved := make(Config, len(fields))
	for _, f := range fields {
		value := saved[f.Key]
		if value == "" {
			value = f.Default
		}
		if f.Check != nil {
			if err := f.Check(value); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Label, err)
			}
		}
		resolved[f.Key] = value
	}
	return resolved, nil
}

// CheckPort accepts a TCP port from 1 to 65535 that is not admission.ProofPort,
// which the admission handshake already serves.
func CheckPort(s string) error {
	port, err := strconv.ParseUint(s, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("invalid TCP port %q", s)
	}
	if uint16(port) == admission.ProofPort {
		return fmt.Errorf("port %d is reserved for admission", port)
	}
	return nil
}
