package app

// reload runs one background read at a time without losing a request. A
// request made while a read runs marks the result stale, and one more read
// follows it, so whatever changed before the last request is always shown, and
// an older read never replaces a newer one. It is used on the UI thread only.
type reload struct {
	running bool
	again   bool
}

// start reports whether a read should begin now. While one runs it returns
// false and asks for another once that one is done.
func (r *reload) start() bool {
	if r.running {
		r.again = true
		return false
	}
	r.running = true
	return true
}

// done ends the running read, and reports whether another was asked for while
// it ran.
func (r *reload) done() bool {
	r.running = false
	again := r.again
	r.again = false
	return again
}

// busy reports whether a read is running.
func (r *reload) busy() bool { return r.running }
