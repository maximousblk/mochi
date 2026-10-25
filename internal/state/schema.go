package state

import (
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations" // PocketBase's own system collections
)

// ensureSchema creates the collections Mochi keeps, those that do not exist
// yet. There are no versioned migrations: a schema change edits this file, and
// existing data is moved by hand. Values are typed fields; Mochi writes no JSON
// fields.
func (s *Store) ensureSchema() error {
	hosts, err := s.ensure("hosts", func(c *core.Collection) {
		// Hosts this machine connects to, in the order they were added.
		c.Fields.Add(
			&core.TextField{Name: "name", Required: true},
			&core.TextField{Name: "address", Required: true},
		)
		c.Indexes = []string{"CREATE UNIQUE INDEX idx_hosts_address ON hosts (address)"}
	})
	if err != nil {
		return err
	}
	defs := []struct {
		name   string
		define func(*core.Collection)
	}{
		{"machine", func(c *core.Collection) {
			// One record (machineID): this machine's identity and server state.
			c.Fields.Add(
				&core.BoolField{Name: "server_enabled"},
				&core.TextField{Name: "server_psk", Hidden: true},
				&core.NumberField{Name: "server_region"},
				&core.TextField{Name: "identity_seed", Hidden: true}, // hex Ed25519 seed
			)
		}},
		{"launchers", func(c *core.Collection) {
			// Each launcher's server-side switch, by lowercase launcher name.
			c.Fields.Add(
				&core.TextField{Name: "name", Required: true},
				&core.BoolField{Name: "serving"},
			)
			c.Indexes = []string{"CREATE UNIQUE INDEX idx_launchers_name ON launchers (name)"}
		}},
		{"launcher_settings", func(c *core.Collection) {
			// Launcher settings, one record per key: the server side when host
			// is empty, otherwise the client side for that host. Removing a host
			// removes its settings.
			c.Fields.Add(
				&core.TextField{Name: "launcher", Required: true},
				&core.RelationField{Name: "host", CollectionId: hosts.Id, MaxSelect: 1, CascadeDelete: true},
				&core.TextField{Name: "key", Required: true},
				&core.TextField{Name: "value"},
			)
			c.Indexes = []string{"CREATE UNIQUE INDEX idx_launcher_settings_pair ON launcher_settings (launcher, host, key)"}
		}},
		{"certs", func(c *core.Collection) {
			// Certificates this machine presents to hosts: hex of the CBOR bytes.
			c.Fields.Add(&core.TextField{Name: "cert", Required: true})
			c.Indexes = []string{"CREATE UNIQUE INDEX idx_certs_cert ON certs (cert)"}
		}},
		{"received_texts", func(c *core.Collection) {
			// Texts that arrived on the Send text port; the newest 100 are kept.
			c.Fields.Add(
				&core.TextField{Name: "sender"},
				&core.TextField{Name: "body"},
				&core.DateField{Name: "received", Required: true},
			)
		}},
		{"clients", func(c *core.Collection) {
			// The admission gate's view of every node that connected to this
			// machine's server (internal/admission).
			c.Fields.Add(
				&core.TextField{Name: "node_key", Required: true},
				&core.SelectField{Name: "state", Required: true, MaxSelect: 1, Values: []string{"probation", "approved", "rejected", "blocked"}},
				&core.TextField{Name: "ca"},
				&core.TextField{Name: "name"},
				&core.DateField{Name: "expires"},
				&core.TextField{Name: "reason"},
				&core.DateField{Name: "first_seen"},
				&core.DateField{Name: "last_seen"},
			)
			c.Indexes = []string{"CREATE UNIQUE INDEX idx_clients_node_key ON clients (node_key)"}
		}},
	}
	for _, d := range defs {
		if _, err := s.ensure(d.name, d.define); err != nil {
			return err
		}
	}
	return nil
}

// ensure returns the collection called name, creating it with define first if
// it does not exist.
func (s *Store) ensure(name string, define func(*core.Collection)) (*core.Collection, error) {
	c, err := s.app.FindCollectionByNameOrId(name)
	if err == nil || !missing(err) {
		return c, err
	}
	c = core.NewBaseCollection(name)
	define(c)
	return c, s.app.Save(c)
}
