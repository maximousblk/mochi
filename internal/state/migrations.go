package state

import (
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	core.AppMigrations.Register(func(app core.App) error {
		hosts := core.NewBaseCollection("hosts")
		hosts.Fields.Add(&core.TextField{Name: "name", Required: true}, &core.TextField{Name: "address", Required: true})
		hosts.Indexes = []string{"CREATE UNIQUE INDEX idx_hosts_address ON hosts (address)"}
		machine := core.NewBaseCollection("machine")
		machine.Fields.Add(&core.BoolField{Name: "server_enabled"}, &core.TextField{Name: "server_psk", Hidden: true}, &core.NumberField{Name: "server_region"}, &core.TextField{Name: "identity_seed", Hidden: true})
		launchers := core.NewBaseCollection("launchers")
		launchers.Fields.Add(&core.TextField{Name: "name", Required: true}, &core.BoolField{Name: "serving"})
		launchers.Indexes = []string{"CREATE UNIQUE INDEX idx_launchers_name ON launchers (name)"}
		settings := core.NewBaseCollection("launcher_settings")
		settings.Fields.Add(&core.TextField{Name: "launcher", Required: true}, &core.RelationField{Name: "host", CollectionId: hosts.Id, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "key", Required: true}, &core.TextField{Name: "value"})
		settings.Indexes = []string{"CREATE UNIQUE INDEX idx_launcher_settings_pair ON launcher_settings (launcher, host, key)"}
		certs := core.NewBaseCollection("certs")
		certs.Fields.Add(&core.TextField{Name: "cert", Required: true})
		certs.Indexes = []string{"CREATE UNIQUE INDEX idx_certs_cert ON certs (cert)"}
		texts := core.NewBaseCollection("received_texts")
		texts.Fields.Add(&core.TextField{Name: "sender"}, &core.TextField{Name: "body"}, &core.DateField{Name: "received", Required: true})
		clients := core.NewBaseCollection("clients")
		clients.Fields.Add(&core.TextField{Name: "node_key", Required: true}, &core.SelectField{Name: "state", Required: true, MaxSelect: 1, Values: []string{"probation", "approved", "rejected", "blocked"}}, &core.TextField{Name: "ca"}, &core.TextField{Name: "name"}, &core.DateField{Name: "expires"}, &core.TextField{Name: "reason"}, &core.DateField{Name: "first_seen"}, &core.DateField{Name: "last_seen"})
		clients.Indexes = []string{"CREATE UNIQUE INDEX idx_clients_node_key ON clients (node_key)"}
		for _, c := range []*core.Collection{hosts, machine, launchers, settings, certs, texts, clients} {
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, name := range []string{"clients", "received_texts", "certs", "launcher_settings", "launchers", "machine", "hosts"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if err := app.Delete(c); err != nil {
				return err
			}
		}
		return nil
	})
}
