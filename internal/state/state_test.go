package state

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteStateRoundTrip(t *testing.T) {
	store, err := OpenState(filepath.Join(t.TempDir(), "mochi.db"), filepath.Join(t.TempDir(), "legacy.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	defer store.Close()
	want := Host{Name: "lab", Address: "tcExampleAddress"}
	if err := store.AddHost(want); err != nil {
		t.Fatalf("add host: %v", err)
	}
	hosts, err := store.Hosts()
	if err != nil {
		t.Fatalf("load hosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != want {
		t.Fatalf("round trip mismatch: got %#v, want %#v", hosts, want)
	}
	if err := store.SetBool("server_enabled", true); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if enabled, err := store.BoolSetting("server_enabled"); err != nil || !enabled {
		t.Fatalf("server setting did not persist: enabled=%v err=%v", enabled, err)
	}
}

func TestIdentityAndServerParamsPersistAcrossStateReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mochi.db")
	first, err := OpenState(path, "")
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := first.Identity()
	if err != nil {
		t.Fatal(err)
	}
	firstPSK, region, err := first.ServerParams()
	if err != nil || region != 0 {
		t.Fatalf("fresh server params: region=%d err=%v", region, err)
	}
	if err := first.SaveRegion(304); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenState(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondID, err := second.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if !firstID.Private().Equal(secondID.Private()) {
		t.Fatal("machine identity changed after reopening the SQLite state")
	}
	secondPSK, region, err := second.ServerParams()
	if err != nil || region != 304 || !firstPSK.Equal(secondPSK) {
		t.Fatalf("server params changed after reopening: region=%d err=%v", region, err)
	}
}

func TestTrustedKeysRejectBadEntries(t *testing.T) {
	good := strings.Repeat("ab", 32)
	for name, tc := range map[string]struct{ json, want string }{
		"ok":        {`{"trusted_keys":[{"name":"a","key":"` + good + `"}]}`, ""},
		"none":      {`{}`, ""},
		"short":     {`{"trusted_keys":[{"name":"a","key":"abcd"}]}`, "lowercase hex"},
		"uppercase": {`{"trusted_keys":[{"name":"a","key":"` + strings.ToUpper(good) + `"}]}`, "lowercase hex"},
		"duplicate": {`{"trusted_keys":[{"name":"a","key":"` + good + `"},{"name":"b","key":"` + good + `"}]}`, "twice"},
	} {
		old := bootstrapData
		bootstrapData = []byte(tc.json)
		_, err := TrustedKeys()
		bootstrapData = old
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: err = %v; want %q", name, err, tc.want)
		}
	}
}
