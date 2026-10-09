package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

type fakeConnector struct {
	name       string
	disableErr error
	calls      int
	beforeDrop func() error
	serverSwitch
}

func (c *fakeConnector) Name() string                { return c.name }
func (c *fakeConnector) ServerServe(*ServerServices) {}
func (c *fakeConnector) Icon() *ui.SVG               { return nil }
func (c *fakeConnector) SetHosts([]Host)             {}
func (c *fakeConnector) Enabled(Host) bool           { return false }
func (c *fakeConnector) Enable(Host) error           { return nil }
func (c *fakeConnector) CLICommand() string          { return "" }
func (c *fakeConnector) RunCommand(context.Context, []string, io.Reader, io.Writer, Dialing) error {
	return nil
}
func (c *fakeConnector) Disable(Host) error {
	c.calls++
	if c.beforeDrop != nil {
		if err := c.beforeDrop(); err != nil {
			return err
		}
	}
	return c.disableErr
}

func TestRemoveHostDisablesEveryConnectorBeforeDeleting(t *testing.T) {
	store, err := OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := Host{Name: "lab", Address: "tcExampleAddress"}
	if err := store.AddHost(host); err != nil {
		t.Fatal(err)
	}
	verifyStillStored := func() error {
		hosts, err := store.Hosts()
		if err != nil {
			return err
		}
		if len(hosts) != 1 || hosts[0] != host {
			return errors.New("host was removed before connectors were disabled")
		}
		return nil
	}
	first := &fakeConnector{name: "first", beforeDrop: verifyStillStored}
	second := &fakeConnector{name: "second", beforeDrop: verifyStillStored}
	a := &app{hosts: []Host{host}, selectedHost: host.Address, store: store, connectors: []Connector{first, second}}
	if err := a.removeHost(0); err != nil {
		t.Fatalf("remove host: %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("disable calls: first=%d second=%d", first.calls, second.calls)
	}
	if len(a.hosts) != 0 || a.selectedHost != "" {
		t.Fatalf("app state was not cleared: hosts=%v selected=%q", a.hosts, a.selectedHost)
	}
	hosts, err := store.Hosts()
	if err != nil || len(hosts) != 0 {
		t.Fatalf("database host was not deleted: hosts=%v err=%v", hosts, err)
	}
}

func TestRemoveHostAttemptsAllConnectorsAndKeepsHostOnDisableFailure(t *testing.T) {
	store, err := OpenState(filepath.Join(t.TempDir(), "mochi.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := Host{Name: "lab", Address: "tcExampleAddress"}
	if err := store.AddHost(host); err != nil {
		t.Fatal(err)
	}
	first := &fakeConnector{name: "first", disableErr: errors.New("cannot disable")}
	second := &fakeConnector{name: "second"}
	a := &app{hosts: []Host{host}, store: store, connectors: []Connector{first, second}}
	if err := a.removeHost(0); err == nil {
		t.Fatal("remove succeeded despite connector disable failure")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("all connectors must be attempted: first=%d second=%d", first.calls, second.calls)
	}
	if len(a.hosts) != 1 {
		t.Fatalf("in-memory host removed on cleanup failure: %v", a.hosts)
	}
	hosts, err := store.Hosts()
	if err != nil || len(hosts) != 1 || hosts[0] != host {
		t.Fatalf("database changed on cleanup failure: hosts=%v err=%v", hosts, err)
	}
}
