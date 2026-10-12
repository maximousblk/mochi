package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
)

// fakeClient is a launcher's client that records when it is disconnected.
type fakeClient struct {
	last          time.Time
	disconnects   int
	disconnectErr error
}

func (c *fakeClient) LastUsed() time.Time { return c.last }

func (c *fakeClient) Disconnect() error {
	c.disconnects++
	return c.disconnectErr
}

// Removing a host disconnects each of its clients, then forgets the host.
func TestRemoveHostDisconnectsItsClients(t *testing.T) {
	store, err := state.OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := state.Host{Name: "lab", Address: testAddress(t)}
	if err := store.AddHost(host); err != nil {
		t.Fatal(err)
	}
	first, second := &fakeClient{}, &fakeClient{}
	a := &app{
		hosts:        []state.Host{host},
		selectedHost: host.Address,
		store:        store,
		connections:  map[string]map[string]connector.Client{host.Address: {"first": first, "second": second}},
	}
	if err := a.removeHost(0); err != nil {
		t.Fatalf("remove host: %v", err)
	}
	if first.disconnects != 1 || second.disconnects != 1 {
		t.Fatalf("disconnects: first=%d second=%d, want 1 each", first.disconnects, second.disconnects)
	}
	if len(a.connections) != 0 || len(a.hosts) != 0 {
		t.Fatalf("connections %v and hosts %v remain after removal", a.connections, a.hosts)
	}
	if stored, _ := store.Hosts(); len(stored) != 0 {
		t.Fatalf("stored hosts = %+v", stored)
	}
}

// A client that cannot disconnect keeps its host: the removal reports why.
func TestRemoveHostKeepsHostWhenDisconnectFails(t *testing.T) {
	store, err := state.OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := state.Host{Name: "lab", Address: testAddress(t)}
	if err := store.AddHost(host); err != nil {
		t.Fatal(err)
	}
	stuck := &fakeClient{disconnectErr: errors.New("cannot disconnect")}
	a := &app{
		hosts:       []state.Host{host},
		store:       store,
		connections: map[string]map[string]connector.Client{host.Address: {"stuck": stuck}},
	}
	if err := a.removeHost(0); err == nil {
		t.Fatal("remove succeeded despite a failed disconnect")
	}
	if len(a.hosts) != 1 {
		t.Fatalf("in-memory host removed on cleanup failure: %v", a.hosts)
	}
	if stored, _ := store.Hosts(); len(stored) != 1 {
		t.Fatalf("stored host removed on cleanup failure: %+v", stored)
	}
}

// The idle reaper disconnects a client whose last data is older than
// idleTimeout, and keeps one that moved data more recently.
func TestReapIdleDisconnectsOnlyStaleClients(t *testing.T) {
	now := time.Now()
	stale := &fakeClient{last: now.Add(-idleTimeout - time.Minute)}
	fresh := &fakeClient{last: now.Add(-time.Hour)}
	a := &app{connections: map[string]map[string]connector.Client{
		"lab": {"SSH": stale, "WebDAV": fresh},
	}}

	a.reapIdle(now)

	if stale.disconnects != 1 {
		t.Fatalf("stale client disconnected %d times, want 1", stale.disconnects)
	}
	if fresh.disconnects != 0 {
		t.Fatalf("fresh client disconnected %d times, want 0", fresh.disconnects)
	}
	if _, ok := a.connections["lab"]["SSH"]; ok {
		t.Fatal("the stale client is still held")
	}
	if _, ok := a.connections["lab"]["WebDAV"]; !ok {
		t.Fatal("the fresh client was dropped")
	}
}

// A host whose last client is reaped leaves no empty entry behind.
func TestReapIdleRemovesEmptyHost(t *testing.T) {
	now := time.Now()
	a := &app{connections: map[string]map[string]connector.Client{
		"lab": {"SSH": &fakeClient{last: now.Add(-idleTimeout - time.Minute)}},
	}}
	a.reapIdle(now)
	if len(a.connections) != 0 {
		t.Fatalf("connections = %v, want none", a.connections)
	}
}
