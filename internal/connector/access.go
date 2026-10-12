package connector

import "sync"

// DiskAccess checks that Mochi may read everything its user can, as a terminal
// with Full Disk Access does. Launchers whose server side reaches the user's
// files check it in Healthy.
type DiskAccess struct {
	has     func() bool
	request func()
	asked   sync.Once
}

// SystemDiskAccess is this machine's. Every launcher shares it, so the user is
// asked once per run however many launchers need the access.
var SystemDiskAccess = NewDiskAccess(hasDiskAccess, requestDiskAccess)

// NewDiskAccess returns a DiskAccess that probes with has, and asks the user
// with request.
func NewDiskAccess(has func() bool, request func()) *DiskAccess {
	return &DiskAccess{has: has, request: request}
}

// Check reports whether Mochi has the access. The first time it finds the access
// missing, it asks the user for it.
func (d *DiskAccess) Check() bool {
	if d.has() {
		return true
	}
	d.asked.Do(d.request)
	return false
}
