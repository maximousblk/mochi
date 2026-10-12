package connector

import "testing"

// Without access, Check fails and asks the user once, however many launchers
// check and however often. Once access is granted, Check passes.
func TestDiskAccessAsksOnce(t *testing.T) {
	granted, asked := false, 0
	d := NewDiskAccess(func() bool { return granted }, func() { asked++ })
	for range 3 {
		if d.Check() {
			t.Fatal("Check passed without access")
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times over three checks; want once", asked)
	}
	granted = true
	if !d.Check() {
		t.Fatal("Check failed after access was granted")
	}
	if asked != 1 {
		t.Fatalf("asked again after access was granted: %d", asked)
	}
}
