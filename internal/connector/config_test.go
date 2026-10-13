package connector

import (
	"errors"
	"strings"
	"testing"
)

// Resolve fills missing and empty keys with defaults, drops keys no field
// declares, and reports the first failing check by the field's label.
func TestResolveCompletesDefaultsAndChecks(t *testing.T) {
	fields := []Field{
		{Key: "port", Label: "Tailcat port", Default: "22022", Check: CheckPort},
		{Key: "user", Label: "User name", Default: ""},
	}

	got, err := Resolve(fields, Config{"port": "2222", "stale": "x", "user": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "2222" || got["user"] != "" || len(got) != 2 {
		t.Fatalf("Resolve = %v; want the saved port, the empty user, and no stale key", got)
	}

	got, err = Resolve(fields, nil)
	if err != nil || got["port"] != "22022" {
		t.Fatalf("Resolve with nothing saved = %v, %v; want the default port", got, err)
	}

	_, err = Resolve(fields, Config{"port": "70000"})
	if err == nil || !strings.Contains(err.Error(), "Tailcat port") {
		t.Fatalf("Resolve with a bad port = %v; want an error naming Tailcat port", err)
	}

	failing := []Field{{Key: "k", Label: "Thing", Check: func(string) error { return errors.New("no") }}}
	if _, err := Resolve(failing, nil); err == nil || !strings.Contains(err.Error(), "Thing") {
		t.Fatalf("Resolve with a failing check = %v; want the label in the error", err)
	}
}
