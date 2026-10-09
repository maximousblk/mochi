package admission

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// ProofPort is the tailcat port a Mochi host answers certificate proofs on.
// Services must not use it.
const ProofPort uint16 = 41414

// Client states. A node may use services only in Approved, and only while a
// trusted CA's certificate for it is unexpired.
const (
	Probation = "probation" // connected, has until expires to be approved
	Approved  = "approved"  // a trusted CA vouched for it until expires
	Rejected  = "rejected"  // quarantine: not approved; forgotten at expires
	Blocked   = "blocked"   // never admitted, whatever certificate it holds, until an admin approves it
)

// A row's ca and name are set when a CA approves the node and survive later
// state changes, so a node that was approved once is "known" until its
// rejected row is forgotten (RejectTTL). Block, or its CA leaving the trusted
// set, clears them. Known nodes never count against the probation slots.
const schema = `
CREATE TABLE IF NOT EXISTS clients (
	node_key   TEXT PRIMARY KEY,
	state      TEXT NOT NULL CHECK (state IN ('probation','approved','rejected','blocked')),
	ca         TEXT NOT NULL DEFAULT '', -- fingerprint of the CA that last approved it; '' = never approved
	name       TEXT NOT NULL DEFAULT '', -- who that certificate was issued to
	expires    INTEGER NOT NULL DEFAULT 0, -- Unix seconds: deadline for the current state; 0 = none
	reason     TEXT NOT NULL DEFAULT '',
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL
)`

const cols = "node_key, state, ca, name, expires, reason, first_seen, last_seen"

// Client is one row of the clients table.
type Client struct {
	Key                 key.NodePublic
	State, CA, Name     string
	Expires             time.Time
	Reason              string
	FirstSeen, LastSeen time.Time
}

const (
	localName = "approved on this host"
	maxProof  = 4 << 10 // bytes in one proof request
	maxCerts  = 8       // certificates in one proof request
)

// Gate decides who may use a tailcat server. Every approval, whether it comes
// from an external CA, from this host's own CA (an admin clicking approve),
// or from a renewal, goes through admit.
type Gate struct {
	ProofPort    uint16
	MaxProbation int           // never-approved nodes allowed to hold a tunnel at once
	ProofTimeout time.Duration // how long a node in probation has to be approved
	RejectTTL    time.Duration // how long a rejected node stays listed
	MaxRejected  int           // never-approved rejected rows kept; the oldest are dropped
	LocalTTL     time.Duration // lifetime of a certificate this host signs for itself
	Logf         func(format string, args ...any)

	srv    *tailcat.Server
	db     *sql.DB
	self   ed25519.PrivateKey
	selfFP string

	mu    sync.Mutex                   // guards db, cas, conns
	cas   map[string]ed25519.PublicKey // by fingerprint; always includes this host's own key
	conns map[key.NodePublic]map[net.Conn]bool
}

// NewGate prepares the clients table. The gate trusts this host's own key
// (self) and the given keys, and nothing else: the trusted set is fixed for
// the life of the gate. At startup, probation rows are dropped as stale, and
// rows approved by a key that is no longer trusted lose that approval.
func NewGate(db *sql.DB, self ed25519.PrivateKey, trusted []ed25519.PublicKey) (*Gate, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`DELETE FROM clients WHERE state = 'probation'`); err != nil {
		return nil, err
	}
	pub := self.Public().(ed25519.PublicKey)
	g := &Gate{
		ProofPort: ProofPort, MaxProbation: 32, ProofTimeout: 20 * time.Second,
		RejectTTL: 24 * time.Hour, MaxRejected: 256, LocalTTL: 24 * time.Hour,
		db: db, self: self, selfFP: Fingerprint(pub),
		cas:   map[string]ed25519.PublicKey{},
		conns: map[key.NodePublic]map[net.Conn]bool{},
	}
	for _, ca := range append([]ed25519.PublicKey{pub}, trusted...) {
		g.cas[Fingerprint(ca)] = ca
	}
	args := []any{}
	marks := ""
	for fp := range g.cas {
		args = append(args, fp)
		marks += ",?"
	}
	untrusted := `ca != '' AND ca NOT IN (` + marks[1:] + `)`
	if _, err := db.Exec(`UPDATE clients SET state = 'rejected', reason = 'CA no longer trusted', expires = `+
		fmt.Sprint(time.Now().Add(g.RejectTTL).Unix())+` WHERE state = 'approved' AND `+untrusted, args...); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`UPDATE clients SET ca = '', name = '' WHERE `+untrusted, args...); err != nil {
		return nil, err
	}
	return g, nil
}

// Install sets s.AllowClient and s.OnTCP; serve returns the handler for a
// service port, as Server.OnTCP does. Every service port is guarded.
// Call it before s.Start.
func (g *Gate) Install(s *tailcat.Server, serve func(port uint16) func(net.Conn)) {
	g.srv = s
	s.AllowClient = g.allowClient
	s.OnTCP = func(port uint16) func(net.Conn) {
		if port == g.ProofPort {
			return g.prove
		}
		h := serve(port)
		if h == nil {
			return nil
		}
		return func(c net.Conn) { g.guard(c, h) }
	}
}

// Run expires approvals, probations and quarantine entries until ctx ends.
func (g *Gate) Run(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.sweep()
		}
	}
}

// Approve is what an admin does to a node it recognizes: sign a short-lived
// certificate with this host's own key and admit it like any other. The host
// renews it before anyone sees it expire (see row), so an approval lasts
// until Block. Approving also lifts a block.
func (g *Gate) Approve(k key.NodePublic) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.get(k).State == Blocked {
		g.exec(`DELETE FROM clients WHERE node_key = ?`, k.String())
	}
	return g.approveLocked(k)
}

// SelfFingerprint names this host's own CA, the one that approves nodes from
// the Settings page.
func (g *Gate) SelfFingerprint() string { return g.selfFP }

// Lookup returns k's stored row, as the clients table holds it. Its State is
// empty for a node the gate has never seen.
func (g *Gate) Lookup(k key.NodePublic) Client {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.get(k)
}

// Block refuses k whatever certificate it presents, and drops it now.
func (g *Gate) Block(k key.NodePublic) {
	g.mu.Lock()
	g.put(Client{Key: k, State: Blocked, Reason: "blocked by admin"})
	g.mu.Unlock()
	g.cut(k)
}

// Admit records cert as an approval of k, as a presented certificate does. It
// fails if the certificate is not signed by a trusted CA or is not for k.
func (g *Gate) Admit(k key.NodePublic, cert []byte) error { return g.admit(k, cert) }

// Consider is tailcat's admission decision for k: true if k may use a service
// now. A stranger gets a probation slot, as it does on connecting.
func (g *Gate) Consider(k key.NodePublic) bool { return g.allowClient(k) }

// Reject quarantines k with reason, as a failed certificate does.
func (g *Gate) Reject(k key.NodePublic, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reject(k, reason)
}

// Clients lists rows in the given state, newest first.
func (g *Gate) Clients(state string) []Client {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.query(`SELECT `+cols+` FROM clients WHERE state = ? ORDER BY last_seen DESC`, state)
}

// admit is the only way a node becomes Approved.
func (g *Gate) admit(k key.NodePublic, cert []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.admitLocked(k, cert)
}

func (g *Gate) admitLocked(k key.NodePublic, cert []byte) error {
	if g.get(k).State == Blocked {
		return errors.New("blocked")
	}
	r, err := Verify(cert, g.caList(), k, time.Now())
	if err != nil {
		// Keep why, for the admin, in case this key times out unapproved.
		g.exec(`UPDATE clients SET reason = ? WHERE node_key = ? AND state = 'probation'`, err.Error(), k.String())
		return err
	}
	if err := g.put(Client{Key: k, State: Approved, CA: Fingerprint(r.CA), Name: r.ID, Expires: r.Exp}); err != nil {
		return err // not approved: the client must not be told it was
	}
	g.logf("approved %v (%s) until %v", k.ShortString(), r.ID, r.Exp)
	return nil
}

// approveLocked signs a certificate for k with this host's own key and admits
// it. Callers hold g.mu.
func (g *Gate) approveLocked(k key.NodePublic) error {
	cert, err := Issue(g.self, k, localName, time.Now().Add(g.LocalTTL))
	if err != nil {
		return err
	}
	return g.admitLocked(k, cert)
}

// row returns k's row as of now. A node this host approved itself never
// lapses: if its approval has run out, it is renewed here, so no caller sees
// it expired. Block is how an admin takes it back. Callers hold g.mu.
func (g *Gate) row(k key.NodePublic, now time.Time) Client {
	c := g.get(k)
	if c.CA == g.selfFP && c.State != Blocked && !g.live(c, now) {
		if g.approveLocked(k) == nil {
			c = g.get(k)
		}
	}
	return c
}

// allowClient is tailcat's admission hook. A node with a live approval is in.
// A blocked node is out. Any other node gets a probation slot, so it has a
// tunnel to present a certificate through. A node that was approved before
// is known: it never counts against the slots, so renewing cannot be starved
// by strangers. Strangers are limited to MaxProbation at once.
func (g *Gate) allowClient(k key.NodePublic) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	c := g.row(k, now)
	switch {
	case g.live(c, now):
		return true
	case c.State == Blocked:
		return false
	case c.State == Probation:
		return true
	case c.CA == "" && g.strangers() >= g.MaxProbation:
		return false
	}
	reason := "no certificate"
	if c.CA != "" {
		reason = "certificate expired"
	}
	return g.setState(k, Probation, reason, now.Add(g.ProofTimeout)) == nil
}

func (g *Gate) live(c Client, now time.Time) bool {
	_, trusted := g.cas[c.CA]
	return c.State == Approved && trusted && now.Before(c.Expires)
}

// prove is the one service an unapproved node can reach. A request is a CBOR
// array of certificates, possibly empty; the reply is "ok" if the node is
// approved afterwards, and "no: " and a reason if not. An empty request just
// asks where the node stands.
func (g *Gate) prove(c net.Conn) {
	defer c.Close()
	k, ok := g.srv.PeerKey(c.RemoteAddr())
	if !ok {
		return
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	var certs [][]byte
	if err := dec.NewDecoder(io.LimitReader(c, maxProof)).Decode(&certs); err != nil || len(certs) > maxCerts {
		io.WriteString(c, "no: bad request")
		return
	}
	var why error
	for _, cert := range certs {
		if why = g.admit(k, cert); why == nil {
			break
		}
	}
	reply := g.status(k, why)
	if strings.HasPrefix(reply, "no: ") {
		// Say which CAs this host trusts, so the client can tell whether any
		// certificate it holds could ever be accepted here.
		var lines []string
		for _, ca := range g.cas {
			lines = append(lines, "\nca: "+hex.EncodeToString(ca))
		}
		sort.Strings(lines)
		reply += strings.Join(lines, "")
	}
	io.WriteString(c, reply)
}

// status is the proof port's reply for k; why is the last error from
// presenting its certificates, if any.
func (g *Gate) status(k key.NodePublic, why error) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	c := g.row(k, now)
	switch {
	case g.live(c, now):
		return "ok" // approved, whatever happened to the certificates it just sent
	case why != nil:
		return "no: " + why.Error()
	}
	return "no: pending approval"
}

// guard runs h for c only if c's peer is approved, and tracks c so a
// revocation can close it.
func (g *Gate) guard(c net.Conn, h func(net.Conn)) {
	k, ok := g.srv.PeerKey(c.RemoteAddr())
	g.mu.Lock()
	ok = ok && g.live(g.row(k, time.Now()), time.Now())
	if ok {
		if g.conns[k] == nil {
			g.conns[k] = map[net.Conn]bool{}
		}
		g.conns[k][c] = true
	}
	g.mu.Unlock()
	if !ok {
		c.Close()
		return
	}
	defer func() {
		g.mu.Lock()
		delete(g.conns[k], c)
		g.mu.Unlock()
	}()
	h(c)
}

// sweep moves rows past their deadline to their next state.
func (g *Gate) sweep() {
	g.mu.Lock()
	var cut []key.NodePublic
	for _, c := range g.query(`SELECT `+cols+` FROM clients WHERE state != 'blocked' AND expires <= ?`, time.Now().Unix()) {
		switch {
		case c.State == Rejected:
			g.exec(`DELETE FROM clients WHERE node_key = ?`, c.Key.String())
		case c.State == Approved && c.CA == g.selfFP:
			g.approveLocked(c.Key) // this host's own approvals renew
		case c.State == Approved:
			g.reject(c.Key, "certificate expired")
			cut = append(cut, c.Key)
		case c.State == Probation:
			g.reject(c.Key, c.Reason)
			cut = append(cut, c.Key)
		}
	}
	g.mu.Unlock()
	g.cut(cut...)
}

// reject quarantines k and keeps the list bounded. Only never-approved rows
// are dropped to make room: a flood of throwaway keys cannot push out a known
// node. Callers hold g.mu.
func (g *Gate) reject(k key.NodePublic, reason string) {
	g.setState(k, Rejected, reason, time.Now().Add(g.RejectTTL))
	g.exec(`DELETE FROM clients WHERE node_key IN (
		SELECT node_key FROM clients WHERE state = 'rejected' AND ca = '' ORDER BY last_seen DESC LIMIT -1 OFFSET ?)`, g.MaxRejected)
}

// cut closes the keys' open connections while they are still routed, so the
// FIN reaches them, then drops them from the server. DisconnectClient alone
// blackholes a peer and leaves its connections stalled.
func (g *Gate) cut(keys ...key.NodePublic) {
	for _, k := range keys {
		g.mu.Lock()
		open := g.conns[k]
		delete(g.conns, k)
		g.mu.Unlock()
		for c := range open {
			c.Close()
		}
		time.AfterFunc(time.Second, func() {
			g.mu.Lock()
			live := g.live(g.get(k), time.Now())
			g.mu.Unlock()
			if !live {
				g.srv.DisconnectClient(k)
			}
		})
	}
}

func (g *Gate) caList() []ed25519.PublicKey {
	out := make([]ed25519.PublicKey, 0, len(g.cas))
	for _, ca := range g.cas {
		out = append(out, ca)
	}
	return out
}

// Database helpers; callers hold g.mu. Errors are logged and returned. The
// admission hook cannot report one, but a failed write leaves the row in its
// old state, which fails closed; admit returns it to the caller. The
// database has one connection, so never hold rows open across another call.

func (g *Gate) exec(q string, args ...any) error {
	_, err := g.db.Exec(q, args...)
	if err != nil {
		g.logf("clients: %v", err)
	}
	return err
}

// put writes a whole row, including who approved it.
func (g *Gate) put(c Client) error {
	now := time.Now().Unix()
	return g.exec(`INSERT INTO clients (`+cols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_key) DO UPDATE SET state = excluded.state, ca = excluded.ca, name = excluded.name,
			expires = excluded.expires, reason = excluded.reason, last_seen = excluded.last_seen`,
		c.Key.String(), c.State, c.CA, c.Name, unix(c.Expires), c.Reason, now, now)
}

// setState moves k to state, leaving who approved it alone.
func (g *Gate) setState(k key.NodePublic, state, reason string, expires time.Time) error {
	now := time.Now().Unix()
	return g.exec(`INSERT INTO clients (node_key, state, reason, expires, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_key) DO UPDATE SET state = excluded.state, reason = excluded.reason,
			expires = excluded.expires, last_seen = excluded.last_seen`,
		k.String(), state, reason, unix(expires), now, now)
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// get returns k's row, or the zero Client (empty State) if there is none.
func (g *Gate) get(k key.NodePublic) Client {
	if rows := g.query(`SELECT `+cols+` FROM clients WHERE node_key = ?`, k.String()); len(rows) > 0 {
		return rows[0]
	}
	return Client{}
}

func (g *Gate) count(state string) (n int) {
	g.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE state = ?`, state).Scan(&n)
	return n
}

// strangers counts the never-approved nodes holding a probation slot.
func (g *Gate) strangers() (n int) {
	g.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE state = 'probation' AND ca = ''`).Scan(&n)
	return n
}

func (g *Gate) query(q string, args ...any) []Client {
	rs, err := g.db.Query(q, args...)
	if err != nil {
		g.logf("clients: %v", err)
		return nil
	}
	defer rs.Close()
	var out []Client
	for rs.Next() {
		var c Client
		var nk string
		var exp, first, last int64
		if err := rs.Scan(&nk, &c.State, &c.CA, &c.Name, &exp, &c.Reason, &first, &last); err != nil {
			g.logf("clients: %v", err)
			continue
		}
		if err := c.Key.UnmarshalText([]byte(nk)); err != nil {
			continue
		}
		c.Expires, c.FirstSeen, c.LastSeen = time.Unix(exp, 0), time.Unix(first, 0), time.Unix(last, 0)
		out = append(out, c)
	}
	return out
}

func (g *Gate) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}

// Refused is a host's answer when it does not approve this machine.
type Refused struct {
	Reason string
	// CAs are the keys the host trusts to approve a machine, as it listed them.
	// Empty if it did not say.
	CAs []ed25519.PublicKey
}

func (r *Refused) Error() string { return "host did not approve this machine: " + r.Reason }

// Prove asks the host c is connected to whether it approves this machine,
// presenting certs (there may be none). It returns nil if the host approves,
// a *Refused if it says no, and another error if the proof port could not be
// used, as on a server that is not a Mochi host.
func Prove(ctx context.Context, c *tailcat.Client, port uint16, certs [][]byte) error {
	if certs == nil {
		certs = [][]byte{}
	}
	req, err := enc.Marshal(certs)
	if err != nil {
		return err
	}
	conn, err := c.DialTCPPort(ctx, port)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write(req); err != nil {
		return err
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetReadDeadline(d)
	}
	reply, err := io.ReadAll(io.LimitReader(conn, 8192))
	if string(reply) == "ok" {
		return nil
	}
	if body, ok := strings.CutPrefix(string(reply), "no: "); ok {
		reason, list, _ := strings.Cut(body, "\n")
		r := &Refused{Reason: reason}
		for _, line := range strings.Split(list, "\n") {
			if h, ok := strings.CutPrefix(line, "ca: "); ok {
				if b, err := hex.DecodeString(h); err == nil && len(b) == ed25519.PublicKeySize {
					r.CAs = append(r.CAs, b)
				}
			}
		}
		return r
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("unexpected reply from the proof port %q: %w", reply, err)
}
