//go:build !darwin

package webdav

// hasDiskAccess reports whether Mochi may read every folder it serves. Only
// macOS gates folders behind a permission.
func hasDiskAccess() bool { return true }

func requestDiskAccess() {}
