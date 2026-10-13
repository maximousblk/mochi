package app

import (
	"errors"
	"time"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
)

// idleTimeout is how long a connection may go unused before it is closed.
const idleTimeout = 6 * time.Hour

// idleCheckInterval is how often connections are checked for idleness.
const idleCheckInterval = time.Minute

// connectOp is the busy key of the connect or disconnect of one launcher to one host.
func connectOp(address, name string) string { return "connect|" + address + "|" + name }

// hostRemoveOp is the busy key of removing the host at address.
func hostRemoveOp(address string) string { return "host-remove|" + address }

// pendingConnect tracks one Connect while it runs off the UI thread. Its client
// is stored only once Connect has returned on the UI thread, but a hand-off can
// fail before that, so failed remembers an error that arrives early.
type pendingConnect struct {
	stored bool
	client connector.Client
	failed error
}

// toggleConnection connects lc to host when it is not connected, and
// disconnects it when it is. Both run off the UI thread as the launcher's busy
// operation, so the tile shows the wait and a second click starts nothing.
func (a *app) toggleConnection(host state.Host, lc connector.Connector) {
	name := lc.Name()
	op := connectOp(host.Address, name)
	if a.busy[op] {
		return
	}
	if _, ok := a.connections[host.Address][name]; ok {
		client := a.detachConnection(host.Address, name)
		a.async(op, client.Disconnect, func(err error) {
			if err != nil {
				a.status = name + " disconnect failed: " + err.Error()
				return
			}
			a.status = name + " disconnected"
		})
		return
	}

	p := &pendingConnect{}
	address := host.Address
	onError := func(err error) {
		a.update(func() { a.handoffFailed(address, name, p, err) })
	}
	var client connector.Client
	var loadErr error
	a.status = name + " connecting…"
	a.async(op, func() error {
		var cfg connector.Config
		cfg, loadErr = a.loadConfig(connector.ClientConfigKey(name, address), lc.ClientFields())
		var err error
		client, err = lc.Connect(host, cfg, onError)
		return err
	}, func(err error) {
		if err != nil {
			a.status = name + " connector failed: " + err.Error()
			return
		}
		p.stored = true
		p.client = client
		if a.quitting || a.busy[hostRemoveOp(address)] || !a.hasHost(address) {
			a.disconnectOff(client, name+" connection dropped")
			return
		}
		if p.failed != nil {
			a.disconnectOff(client, name+" stopped")
			a.status = name + " stopped: " + p.failed.Error()
			return
		}
		if a.connections == nil {
			a.connections = map[string]map[string]connector.Client{}
		}
		if a.connections[address] == nil {
			a.connections[address] = map[string]connector.Client{}
		}
		a.connections[address][name] = client
		a.status = name + " connected"
		if loadErr != nil {
			a.status = name + " connected with default settings: " + loadErr.Error()
		}
	})
}

// handoffFailed runs on the UI thread when a launcher's hand-off fails after its
// Connect returned. It drops the connection, because the tunnel is no longer
// useful to the user.
func (a *app) handoffFailed(address, name string, p *pendingConnect, err error) {
	if !p.stored {
		if p.failed == nil {
			p.failed = err
		}
		return
	}
	if a.connections[address][name] != p.client {
		return
	}
	client := a.detachConnection(address, name)
	a.disconnectOff(client, name+" stopped")
	a.status = name + " stopped: " + err.Error()
}

// detachConnection removes the connection of name to address from the table and
// returns it, or nil when there is none. The caller disconnects it.
func (a *app) detachConnection(address, name string) connector.Client {
	client := a.connections[address][name]
	delete(a.connections[address], name)
	if len(a.connections[address]) == 0 {
		delete(a.connections, address)
	}
	return client
}

// detachHost removes every connection to the host at address and returns them.
// The caller disconnects them off the UI thread.
func (a *app) detachHost(address string) []connector.Client {
	var clients []connector.Client
	for name, client := range a.connections[address] {
		clients = append(clients, client)
		delete(a.connections[address], name)
	}
	delete(a.connections, address)
	return clients
}

// disconnectOff disconnects client off the UI thread and reports a failure in
// the status bar. label names the connection in that report.
func (a *app) disconnectOff(client connector.Client, label string) {
	go func() {
		err := client.Disconnect()
		a.update(func() {
			if err != nil {
				a.status = label + ": disconnect failed: " + err.Error()
			}
		})
	}()
}

// disconnectAll closes every connection, as the app quits. It runs before the
// store closes, so it waits for each Disconnect.
func (a *app) disconnectAll() error {
	var errs []error
	for address := range a.connections {
		for _, client := range a.detachHost(address) {
			if err := client.Disconnect(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// watchIdle asks the UI thread to close idle connections every idleCheckInterval
// until the app quits.
func (a *app) watchIdle() {
	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		a.update(func() {
			if a.quitting {
				return
			}
			a.reapIdle(time.Now())
		})
	}
}

// reapIdle closes connections unused for longer than idleTimeout. It runs on the
// UI thread, and the disconnects run off it.
func (a *app) reapIdle(now time.Time) {
	for address, byName := range a.connections {
		for name, client := range byName {
			if now.Sub(client.LastUsed()) <= idleTimeout {
				continue
			}
			a.detachConnection(address, name)
			a.disconnectOff(client, name+" idle")
			a.status = name + " disconnected after being idle"
		}
	}
}

// hasHost reports whether a host with address is still in the sidebar.
func (a *app) hasHost(address string) bool {
	for _, host := range a.hosts {
		if host.Address == address {
			return true
		}
	}
	return false
}
