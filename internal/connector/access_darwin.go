package connector

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
)

// tccDatabase is readable only by processes with Full Disk Access, and exists on
// every Mac, so opening it tells whether Mochi has that access.
const tccDatabase = "/Library/Application Support/com.apple.TCC/TCC.db"

// fullDiskAccessPane is the Full Disk Access list in System Settings.
const fullDiskAccessPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"

// hasDiskAccess reports whether Mochi has Full Disk Access. It covers every
// folder macOS protects: Desktop, Documents, Downloads, iCloud Drive, network
// and removable volumes, and other apps' data. Without it, macOS refuses those
// to Mochi and to the shells it starts.
func hasDiskAccess() bool {
	f, err := os.Open(tccDatabase)
	if err == nil {
		f.Close()
		return true
	}
	return !errors.Is(err, fs.ErrPermission)
}

// requestDiskAccess opens Full Disk Access in System Settings. macOS has no
// prompt for it, so the user turns Mochi on there.
func requestDiskAccess() {
	if cmd := exec.Command("open", fullDiskAccessPane); cmd.Start() == nil {
		go cmd.Wait()
	}
}
