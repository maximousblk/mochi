package connector

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ashwin-y/mochi/internal/admission"
)

// tunnelDialTimeout bounds how long a tunnel waits for the host to accept a
// connection.
const tunnelDialTimeout = 15 * time.Second

// Tunnel is a launcher's client for one port of one host. It listens on a
// loopback port, and carries each connection made to that port over Tailcat to
// the host's port. Local programs such as ssh and RustDesk reach the host
// through it.
type Tunnel struct {
	client   *admission.Dialer
	port     uint16
	listener net.Listener

	mu     sync.Mutex
	conns  map[net.Conn]bool // both sides of each carried connection still open
	open   int               // carried connections not yet finished
	last   time.Time         // the last time data moved in either direction
	closed bool
}

// OpenTunnel listens on a free loopback port and carries each connection to it
// to port on the host that client dials. The tunnel takes ownership of client:
// Disconnect closes it, and so does a failed OpenTunnel.
func OpenTunnel(client *admission.Dialer, port uint16) (*Tunnel, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, err
	}
	t := &Tunnel{
		client:   client,
		port:     port,
		listener: ln,
		conns:    map[net.Conn]bool{},
		last:     time.Now(),
	}
	go t.accept()
	return t, nil
}

// Port is the loopback port the tunnel listens on.
func (t *Tunnel) Port() int {
	return t.listener.Addr().(*net.TCPAddr).Port
}

// LastUsed is the last time data moved through the tunnel. While a connection
// is open, it is time.Now().
func (t *Tunnel) LastUsed() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open > 0 {
		return time.Now()
	}
	return t.last
}

// Disconnect stops the listener, closes every open connection and closes the
// Tailcat client. It is idempotent.
func (t *Tunnel) Disconnect() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	t.listener.Close()
	for _, c := range conns {
		c.Close()
	}
	t.client.Close()
	return nil
}

func (t *Tunnel) accept() {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.carry(local)
	}
}

// carry connects local to the host's port and copies data both ways until both
// directions end.
func (t *Tunnel) carry(local net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), tunnelDialTimeout)
	remote, err := t.client.DialTCPPort(ctx, t.port)
	cancel()
	if err != nil {
		log.Printf("tunnel: dial host port %d: %v", t.port, err)
		local.Close()
		return
	}
	if !t.track(local, remote) {
		local.Close()
		remote.Close()
		return
	}
	Splice(local, remote, t.touch)
	t.untrack(local, remote)
}

// track records a carried connection as open. It reports false if the tunnel is
// already disconnected.
func (t *Tunnel) track(local, remote net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.conns[local] = true
	t.conns[remote] = true
	t.open++
	return true
}

// untrack forgets a finished connection.
func (t *Tunnel) untrack(local, remote net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, local)
	delete(t.conns, remote)
	t.open--
}

// touch records that data moved now.
func (t *Tunnel) touch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
}

// Splice copies data both ways between a and b, and closes both once neither
// direction has anything left to copy. If onData is not nil, it runs each time
// data is read from either side.
func Splice(a, b net.Conn, onData func()) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyData(b, a, onData)
		halfClose(b)
	}()
	go func() {
		defer wg.Done()
		copyData(a, b, onData)
		halfClose(a)
	}()
	wg.Wait()
	a.Close()
	b.Close()
}

func copyData(dst io.Writer, src io.Reader, onData func()) {
	if onData != nil {
		src = notify{src, onData}
	}
	io.Copy(dst, src)
}

// notify is a reader that calls f after each read that returns data.
type notify struct {
	r io.Reader
	f func()
}

func (n notify) Read(p []byte) (int, error) {
	c, err := n.r.Read(p)
	if c > 0 {
		n.f()
	}
	return c, err
}

// halfClose ends the writing side of conn, where conn supports it, so the far
// side sees the end of the data while it can still send.
func halfClose(conn net.Conn) {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
