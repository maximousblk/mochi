package share

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddressUsesUNCPathOnWindows(t *testing.T) {
	got := Address("windows", 4000, "tok", "http://127.0.0.1:4000/tok/")
	if want := `\\127.0.0.1@4000\DavWWWRoot\tok`; got != want {
		t.Fatalf("Address = %q, want %q", got, want)
	}
}

func TestAddressIsTheHTTPURLElsewhere(t *testing.T) {
	const u = "http://127.0.0.1:4000/tok/"
	if got := Address("darwin", 4000, "tok", u); got != u {
		t.Fatalf("Address = %q, want %q", got, u)
	}
}

func TestRedactPathHidesOnlyTheSharePrefix(t *testing.T) {
	cases := []struct{ path, prefix, want string }{
		{"/tok/a.txt", "/tok", "/<share>/a.txt"},
		{"/tok", "/tok", "/<share>"},
		{"/other/a.txt", "/tok", "/other/a.txt"},
		{"/tok/a.txt", "", "/tok/a.txt"},
	}
	for _, c := range cases {
		if got := RedactPath(c.path, c.prefix); got != c.want {
			t.Errorf("RedactPath(%q, %q) = %q, want %q", c.path, c.prefix, got, c.want)
		}
	}
}

func TestRequirePrefixServesOnlyTheShare(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequirePrefix("/tok", inner)

	cases := []struct {
		path string
		want int
	}{
		{"/tok", http.StatusOK},
		{"/tok/", http.StatusOK},
		{"/tok/a.txt", http.StatusOK},
		{"/tokx/a.txt", http.StatusNotFound}, // a sibling that only shares the prefix
		{"/other/a.txt", http.StatusNotFound},
		{"/tok/../etc", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		if rec.Code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, rec.Code, c.want)
		}
	}
}

func TestRootProbeAnswersWindowsRedirectorOnly(t *testing.T) {
	h := RequirePrefix("/tok", http.NotFoundHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("DAV"), "1") {
		t.Fatalf("OPTIONS / = %d DAV=%q, want 200 advertising DAV", rec.Code, rec.Header().Get("DAV"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/", nil))
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND / = %d, want 207", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<D:multistatus") {
		t.Fatalf("PROPFIND / body = %q, want a multistatus", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET / = %d, want 404: the root reveals nothing else", rec.Code)
	}
}

func TestRandomTokenIsUniqueAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 20 {
			t.Fatalf("token %q is too short to guess", tok)
		}
		if strings.ContainsAny(tok, "/+=") {
			t.Fatalf("token %q is not URL-safe", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q repeated", tok)
		}
		seen[tok] = true
	}
}

func TestLogRequestsRecordsRedactedPathAndStatus(t *testing.T) {
	prev := Verbose
	Verbose = true
	t.Cleanup(func() { Verbose = prev })

	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	h := LogRequests(logf, "/tok", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/tok/secret.txt", nil))

	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1: %q", len(lines), lines)
	}
	if strings.Contains(lines[0], "/tok") {
		t.Fatalf("log line leaks the share token: %q", lines[0])
	}
	if !strings.Contains(lines[0], "/<share>/secret.txt") || !strings.Contains(lines[0], "-> 201") {
		t.Fatalf("log line = %q, want the redacted path and status 201", lines[0])
	}
}

func TestLogRequestsIsSilentUnlessVerbose(t *testing.T) {
	prev := Verbose
	Verbose = false
	t.Cleanup(func() { Verbose = prev })

	logged := false
	h := LogRequests(func(string, ...any) { logged = true }, "/tok", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/tok/a", nil))
	if logged {
		t.Fatal("a successful request was logged without --verbose")
	}
}
