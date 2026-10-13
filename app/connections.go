package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
)

// idleTimeout is how long a client may go without moving data before Mochi
// disconnects it.
const idleTimeout = 6 * time.Hour

// idleCheckInterval is how often idle clients are looked for.
const idleCheckInterval = time.Minute

// toggleConnection connects lc to host if it is not connected, and disconnects
// it if it is. It says what happened.
func (a *app) toggleConnection(host state.Host, lc connector.Connector) {
	name := lc.Name()
	if _, ok := a.connections[host.Address][name]; ok {
		if err := a.dropConnection(host.Address, name); err != nil {
			a.status = name + " disconnect failed: " + err.Error()
		} else {
			a.status = name + " disconnected"
		}
		return
	}
	cfg := a.configFor(connector.ClientConfigKey(name, host.Address), name, lc.ClientFields())
	client, err := lc.Connect(host, cfg)
	if err != nil {
		a.status = name + " connector failed: " + err.Error()
		return
	}
	if a.connections[host.Address] == nil {
		a.connections[host.Address] = map[string]connector.Client{}
	}
	a.connections[host.Address][name] = client
	a.status = name + " connected"
}

// dropConnection forgets the client of the named launcher on host, and
// disconnects it.
func (a *app) dropConnection(address, name string) error {
	client, ok := a.connections[address][name]
	if !ok {
		return nil
	}
	delete(a.connections[address], name)
	if len(a.connections[address]) == 0 {
		delete(a.connections, address)
	}
	return client.Disconnect()
}

// disconnectHost disconnects every client of host. It tries them all, and
// returns the errors together.
func (a *app) disconnectHost(address string) error {
	var errs []error
	for name := range a.connections[address] {
		if err := a.dropConnection(address, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// disconnectAll disconnects every client, as the app quits.
func (a *app) disconnectAll() {
	for address := range a.connections {
		a.disconnectHost(address)
	}
}

// watchIdle disconnects idle clients every idleCheckInterval, for as long as the
// app runs. The check itself runs on the UI thread, where the clients are kept.
func (a *app) watchIdle() {
	for {
		time.Sleep(idleCheckInterval)
		a.update(func() { a.reapIdle(time.Now()) })
	}
}

// reapIdle disconnects each client that has not moved data for longer than
// idleTimeout as of now.
func (a *app) reapIdle(now time.Time) {
	for address, byName := range a.connections {
		for name, client := range byName {
			if now.Sub(client.LastUsed()) <= idleTimeout {
				continue
			}
			if err := a.dropConnection(address, name); err != nil {
				a.status = name + " idle disconnect failed: " + err.Error()
			} else {
				a.status = name + " disconnected after being idle"
			}
		}
	}
}
