package state

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestStoreRoundTripsHostsAndServerSettings(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := Host{Name: "lab", Address: "tcExampleAddress"}
	if err := store.AddHost(want); err != nil {
		t.Fatal(err)
	}
	hosts, err := store.Hosts()
	if err != nil || len(hosts) != 1 || hosts[0] != want {
		t.Fatalf("hosts = %#v, %v; want %#v", hosts, err, want)
	}
	if err := store.SetServerEnabled(true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.ServerEnabled(); err != nil || !enabled {
		t.Fatalf("server enabled = %v, %v", enabled, err)
	}
}

func TestMachineIdentityAndServerParamsPersist(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := first.Identity()
	if err != nil {
		t.Fatal(err)
	}
	firstPSK, region, err := first.ServerParams()
	if err != nil || region != 0 {
		t.Fatalf("fresh params: region=%d err=%v", region, err)
	}
	if err := first.SaveRegion(304); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondID, err := second.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if !firstID.Private().Equal(secondID.Private()) {
		t.Fatal("machine identity changed after reopening")
	}
	secondPSK, region, err := second.ServerParams()
	if err != nil || region != 304 || !firstPSK.Equal(secondPSK) {
		t.Fatalf("server params changed after reopen: region=%d err=%v", region, err)
	}
}

func TestCertificateRecordsUseHexAndDeduplicate(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cert := []byte{0, 1, 2, 255}
	if err := store.AddCert(cert); err != nil {
		t.Fatal(err)
	}
	if err := store.AddCert(cert); err != nil {
		t.Fatal(err)
	}
	got, err := store.Certs()
	if err != nil || len(got) != 1 || hex.EncodeToString(got[0]) != "000102ff" {
		t.Fatalf("certs=%x err=%v", got, err)
	}
	if err := store.DeleteCerts(); err != nil {
		t.Fatal(err)
	}
	got, err = store.Certs()
	if err != nil || len(got) != 0 {
		t.Fatalf("certs after delete=%x err=%v", got, err)
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
			t.Errorf("%s: err=%v; want %q", name, err, tc.want)
		}
	}
}
