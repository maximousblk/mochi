//go:build !darwin

package connector

// hasDiskAccess reports whether Mochi may read everything its user can. Only
// macOS gates folders behind a permission.
func hasDiskAccess() bool { return true }

func requestDiskAccess() {}
