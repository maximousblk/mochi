package webdav

import (
	"errors"
	"io/fs"
	"os"

	"github.com/ashwin-y/mochi/internal/share"
)

// tccDatabase is readable only by processes with Full Disk Access, and exists on
// every Mac, so opening it tells whether Mochi has that access.
const tccDatabase = "/Library/Application Support/com.apple.TCC/TCC.db"

// fullDiskAccessPane is the Full Disk Access list in System Settings.
const fullDiskAccessPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"

// hasDiskAccess reports whether Mochi may read every folder it serves. Without
// Full Disk Access, macOS refuses the protected folders of the home directory,
// such as Desktop, Documents and Downloads, and Finder fails to list them.
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
	_ = share.StartProcess("open", fullDiskAccessPane)
}
