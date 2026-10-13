package app

import (
	"fmt"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// configTarget is what the Configure dialog edits: the server side of a launcher
// on this machine, or one launcher's client side for one host.
type configTarget struct {
	title  string
	fields []connector.Field
	// server is set for a server target.
	server connector.Server
	// name and host identify a client target.
	name string
	host state.Host
}

func serverTarget(s connector.Server) configTarget {
	return configTarget{title: s.Name() + " server", fields: s.ServerFields(), server: s}
}

func clientTarget(name string, host state.Host, fields []connector.Field) configTarget {
	return configTarget{title: name + " for " + host.Name, fields: fields, name: name, host: host}
}

// key is the settings key the target's config is saved under.
func (t configTarget) key() string {
	if t.server != nil {
		return connector.ServerConfigKey(t.server)
	}
	return connector.ClientConfigKey(t.name, t.host.Address)
}

// configForm is the Configure dialog while it is open. values holds the text of
// each field, in the order of target.fields.
type configForm struct {
	open   bool
	target configTarget
	values []string
	err    string
}

// configFor returns the settings saved under key, completed with defaults. A
// saved config that no longer resolves, such as a folder that was removed, falls
// back to the defaults and says so in the status bar, so the launcher still
// starts.
func (a *app) configFor(key, label string, fields []connector.Field) connector.Config {
	saved, err := a.store.Config(key)
	if err == nil {
		var cfg connector.Config
		if cfg, err = connector.Resolve(fields, saved); err == nil {
			return cfg
		}
	}
	a.status = label + " settings not loaded, using defaults: " + err.Error()
	defaults := connector.Config{}
	for _, f := range fields {
		defaults[f.Key] = f.Default
	}
	return defaults
}

// deleteClientConfigs forgets the client settings every launcher kept for the
// host at address, so a removed host leaves nothing behind.
func (a *app) deleteClientConfigs(address string) error {
	for _, s := range a.servers {
		if err := a.store.DeleteSetting(connector.ClientConfigKey(s.Name(), address)); err != nil {
			return fmt.Errorf("delete %s settings for host: %w", s.Name(), err)
		}
	}
	return nil
}

// openConfig opens the Configure dialog on t, showing the settings it has now.
func (a *app) openConfig(t configTarget) {
	current := a.configFor(t.key(), t.title, t.fields)
	values := make([]string, len(t.fields))
	for i, f := range t.fields {
		values[i] = current[f.Key]
	}
	a.config = configForm{open: true, target: t, values: values}
}

// configDialog shows the Configure dialog while a.config is open. Each field's
// placeholder is its default, and its help text sits under it.
func (a *app) configDialog(c *ui.Context) {
	f := &a.config
	ui.Modal(c, &f.open, func() {
		theme := c.Theme()
		ui.Text(c, f.target.title).FontSize(18).Bold()
		for i, field := range f.target.fields {
			ui.TextInput(c, &f.values[i]).Placeholder(field.Default).Label(field.Label)
			if field.Help != "" {
				ui.Text(c, field.Help).TextColor(theme.TextMuted)
			}
		}
		if f.err != "" {
			ui.Text(c, f.err).TextColor(theme.Danger)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "Save").Clicked() {
				a.saveConfig()
			}
			if ui.Button(c, "Reset to defaults").Clicked() {
				for i, field := range f.target.fields {
					f.values[i] = field.Default
				}
				f.err = ""
			}
			if ui.Button(c, "Cancel").Clicked() {
				f.open = false
			}
		})
	})
	if !f.open {
		f.err = ""
	}
}

// saveConfig applies the dialog's values. A value that fails its check keeps the
// dialog open with the error, and changes nothing.
func (a *app) saveConfig() {
	f := &a.config
	raw := connector.Config{}
	for i, field := range f.target.fields {
		raw[field.Key] = f.values[i]
	}
	cfg, err := connector.Resolve(f.target.fields, raw)
	if err != nil {
		f.err = err.Error()
		return
	}
	t := f.target
	if t.server != nil {
		if err := a.applyServerConfig(t.server, cfg); err != nil {
			f.err = err.Error()
			return
		}
		a.checkHealth()
		a.status = t.server.Name() + " server settings saved"
	} else {
		if err := a.store.SetConfig(t.key(), cfg); err != nil {
			f.err = "save settings: " + err.Error()
			return
		}
		a.status = t.name + " settings saved"
		if _, connected := a.connections[t.host.Address][t.name]; connected {
			a.status += "; they apply the next time you connect"
		}
	}
	f.open, f.err = false, ""
}

// applyServerConfig saves cfg for server s. If s is serving, it restarts on cfg
// first, and saves cfg only once it serves with it. If cfg does not listen, the
// previous config is listened again, so the server stays up.
func (a *app) applyServerConfig(s connector.Server, cfg connector.Config) error {
	key := connector.ServerConfigKey(s)
	name := s.Name()
	if !a.serving[name] {
		return a.store.SetConfig(key, cfg)
	}
	previous := a.configFor(key, name, s.ServerFields())
	if err := s.Stop(); err != nil {
		return fmt.Errorf("%s server not stopped: %w", name, err)
	}
	if err := s.Listen(cfg); err != nil {
		if rerr := s.Listen(previous); rerr != nil {
			a.serving[name] = false
			return fmt.Errorf("%s server not started: %v; the previous settings did not start it either: %v", name, err, rerr)
		}
		return fmt.Errorf("%s server not started, still serving with the previous settings: %w", name, err)
	}
	return a.store.SetConfig(key, cfg)
}
