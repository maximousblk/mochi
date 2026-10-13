package app

import (
	"fmt"

	"github.com/ashwin-y/mochi/internal/connector"
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo/ui"
)

// configTarget is what a Configure window edits: the server side of a launcher
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

// dialogKey is the identity of the target's Configure window. A target has one
// settings key, so it has one window.
func (t configTarget) dialogKey() string { return "configure|" + t.key() }

// hostAddress is the address of the host the target concerns, or "" for a
// server target, which concerns no host.
func (t configTarget) hostAddress() string {
	if t.server != nil {
		return ""
	}
	return t.host.Address
}

// configDialog is the state of one open Configure window. values holds the text
// of each field, in the order of target.fields, and err is the last failed save.
type configDialog struct {
	target configTarget
	values []string
	err    string
}

// loadConfig returns the settings saved under key, completed with defaults. A
// saved config that no longer resolves, such as a folder that was removed, falls
// back to the defaults, and the error says why. It reads the store, so callers
// run it off the UI thread.
func (a *app) loadConfig(key string, fields []connector.Field) (connector.Config, error) {
	saved, err := a.store.Config(key)
	if err == nil {
		var cfg connector.Config
		if cfg, err = connector.Resolve(fields, saved); err == nil {
			return cfg, nil
		}
	}
	defaults := connector.Config{}
	for _, f := range fields {
		defaults[f.Key] = f.Default
	}
	return defaults, err
}

// deleteClientConfigs forgets the client settings every launcher kept for the
// host at address, so a removed host leaves nothing behind. It reads and writes
// the store, so callers run it off the UI thread.
func (a *app) deleteClientConfigs(address string) error {
	for _, s := range a.servers {
		if err := a.store.DeleteSetting(connector.ClientConfigKey(s.Name(), address)); err != nil {
			return fmt.Errorf("delete %s settings for host: %w", s.Name(), err)
		}
	}
	return nil
}

// openConfig opens the Configure window on t, showing the settings it has now.
// The settings are read off the UI thread, so the window opens once they are
// loaded. A window already open for t is focused instead.
func (a *app) openConfig(t configTarget) {
	if d, ok := a.dialogs[t.dialogKey()]; ok {
		d.win.Focus()
		return
	}
	var cfg map[string]string
	a.async(t.dialogKey(), func() (err error) {
		cfg, err = a.loadConfig(t.key(), t.fields)
		return err
	}, func(err error) {
		if err != nil {
			a.status = t.title + " settings not loaded, using defaults: " + err.Error()
		}
		d := &configDialog{target: t, values: make([]string, len(t.fields))}
		for i, field := range t.fields {
			d.values[i] = cfg[field.Key]
		}
		a.openDialog(t.dialogKey(), t.hostAddress(), t.title, 360, func(c *ui.Context) {
			a.configView(c, d)
		})
	})
}

// configView draws a Configure window. Each field's placeholder is its default,
// and its help text sits under it. Save is disabled while the save runs.
func (a *app) configView(c *ui.Context, d *configDialog) {
	theme := c.Theme()
	busy := a.busy[d.target.dialogKey()]
	for i, field := range d.target.fields {
		ui.TextInput(c, &d.values[i]).Placeholder(field.Default).Label(field.Label)
		if field.Help != "" {
			ui.Text(c, field.Help).TextColor(theme.TextMuted)
		}
	}
	if d.err != "" {
		ui.Text(c, d.err).TextColor(theme.Danger)
	}
	ui.Row(c).Gap(8).Children(func() {
		save := "Save"
		if busy {
			save = "Saving…"
		}
		if ui.PrimaryButton(c, save).Disabled(busy).Clicked() {
			a.saveConfig(d)
		}
		if ui.Button(c, "Reset to defaults").Disabled(busy).Clicked() {
			for i, field := range d.target.fields {
				d.values[i] = field.Default
			}
			d.err = ""
		}
		if ui.Button(c, "Cancel").Clicked() {
			a.closeDialog(d.target.dialogKey())
		}
	})
}

// saveConfig applies the dialog's values off the UI thread. A value that fails
// its check keeps the dialog open with the error and changes nothing. A save
// that succeeds closes the window.
func (a *app) saveConfig(d *configDialog) {
	t := d.target
	raw := connector.Config{}
	for i, field := range t.fields {
		raw[field.Key] = d.values[i]
	}
	cfg, err := connector.Resolve(t.fields, raw)
	if err != nil {
		d.err = err.Error()
		return
	}
	key := t.dialogKey()
	if t.server == nil {
		a.async(key, func() error { return a.store.SetConfig(t.key(), cfg) }, func(err error) {
			if err != nil {
				d.err = "save settings: " + err.Error()
				return
			}
			a.status = t.name + " settings saved"
			if _, connected := a.connections[t.host.Address][t.name]; connected {
				a.status += "; they apply the next time you connect"
			}
			a.closeDialog(key)
		})
		return
	}
	s := t.server
	wasServing := a.serving[s.Name()]
	var serving bool
	a.async(key, func() error {
		var err error
		serving, err = a.applyServerConfig(s, cfg, wasServing)
		return err
	}, func(err error) {
		a.serving[s.Name()] = serving
		if err != nil {
			d.err = err.Error()
			return
		}
		a.checkHealth()
		a.status = s.Name() + " server settings saved"
		a.closeDialog(key)
	})
}

// applyServerConfig saves cfg for server s and reports whether s serves
// afterwards. A serving s restarts on cfg first, and cfg is saved only once s
// serves with it. If cfg does not listen, the previous config is listened again,
// so the server stays up. It runs off the UI thread.
func (a *app) applyServerConfig(s connector.Server, cfg connector.Config, serving bool) (bool, error) {
	key := connector.ServerConfigKey(s)
	name := s.Name()
	if !serving {
		return false, a.store.SetConfig(key, cfg)
	}
	previous, _ := a.loadConfig(key, s.ServerFields())
	if err := s.Stop(); err != nil {
		return true, fmt.Errorf("%s server not stopped: %w", name, err)
	}
	if err := s.Listen(cfg); err != nil {
		if rerr := s.Listen(previous); rerr != nil {
			return false, fmt.Errorf("%s server not started: %v; the previous settings did not start it either: %v", name, err, rerr)
		}
		return true, fmt.Errorf("%s server not started, still serving with the previous settings: %w", name, err)
	}
	return true, a.store.SetConfig(key, cfg)
}
