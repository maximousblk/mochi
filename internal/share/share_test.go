package share

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddressUsesUNCPathOnWindows(t *testing.T) {
	if got, want := Address("windows", 4000), `\\127.0.0.1@4000\DavWWWRoot`; got != want {
		t.Fatalf("Address = %q, want %q", got, want)
	}
}

func TestAddressIsTheRootHTTPURLElsewhere(t *testing.T) {
	const want = "http://127.0.0.1:4000/"
	for _, goos := range []string{"darwin", "linux"} {
		if got := Address(goos, 4000); got != want {
			t.Fatalf("Address(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestLogRequestsRecordsPathAndStatus(t *testing.T) {
	prev := Verbose
	Verbose = true
	t.Cleanup(func() { Verbose = prev })

	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	h := LogRequests(logf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/docs/secret.txt", nil))

	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "PUT /docs/secret.txt") || !strings.Contains(lines[0], "-> 201") {
		t.Fatalf("log line = %q, want the path and status 201", lines[0])
	}
}

func TestLogRequestsIsSilentUnlessVerbose(t *testing.T) {
	prev := Verbose
	Verbose = false
	t.Cleanup(func() { Verbose = prev })

	logged := false
	h := LogRequests(func(string, ...any) { logged = true }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/a", nil))
	if logged {
		t.Fatal("a successful request was logged without --verbose")
	}
}
