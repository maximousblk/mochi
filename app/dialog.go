package app

import (
	"github.com/ashwin-y/mochi/internal/state"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// dialogWidth is how wide every dialog window is, in pixels.
const dialogWidth = 440

// dialogWindow is the native window a dialog lives in. *mygo.Window satisfies
// it. Tests replace the window with a fake, through app.openWindow, so they can
// draw a dialog's view with a ui.Tester.
type dialogWindow interface {
	Update(fn func())
	Invalidate()
	Focus()
	Close()
	OnClosed(fn func()) (off func())
}

// dialog is one open dialog window. host is the address of the host the dialog
// concerns, or "" when it concerns this machine, so removing the host closes it.
type dialog struct {
	win  dialogWindow
	host string
}

// openDialog opens the dialog identified by key in its own window, or focuses
// the window already open for key, so one dialog is never open twice. view
// draws the dialog from the state its closure holds, so each dialog keeps its
// own draft instead of sharing one with the others.
func (a *app) openDialog(key, host, title string, height int, view func(c *ui.Context)) {
	if d, ok := a.dialogs[key]; ok {
		d.win.Focus()
		return
	}
	content := func(c *ui.Context) {
		useTheme(c)
		view(c)
	}
	var win dialogWindow
	if a.openWindow != nil {
		win = a.openWindow(title, dialogWidth, height, content)
	} else {
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:         title,
			Width:         dialogWidth,
			Height:        height,
			DisableResize: true,
			Content:       ui.View(content),
		})
	}
	if a.dialogs == nil {
		a.dialogs = map[string]dialog{}
	}
	a.dialogs[key] = dialog{win: win, host: host}
	// The native close button discards the dialog. The identity check keeps a
	// window that closes late from removing a newer dialog with the same key.
	win.OnClosed(func() {
		if a.dialogs[key].win == win {
			delete(a.dialogs, key)
		}
	})
}

// closeDialog closes the dialog identified by key, if it is open.
func (a *app) closeDialog(key string) {
	d, ok := a.dialogs[key]
	if !ok {
		return
	}
	delete(a.dialogs, key)
	d.win.Close()
}

// closeHostDialogs closes every dialog about the host at address. It runs when
// the host is removed, because the dialogs' targets no longer exist.
func (a *app) closeHostDialogs(address string) {
	for key, d := range a.dialogs {
		if d.host == address {
			a.closeDialog(key)
		}
	}
}

// closeAllDialogs closes every dialog window, as the app quits.
func (a *app) closeAllDialogs() {
	for key := range a.dialogs {
		a.closeDialog(key)
	}
}

// useTheme gives a dialog window's view the app's font, as the main window has.
func useTheme(c *ui.Context) {
	theme := *c.Theme()
	theme.Font = "Ioskeley Mono"
	c.SetTheme(&theme)
}

// async runs work off the UI thread as the operation op, then runs done on the
// UI thread with work's error. While op is busy the view shows it, and async
// starts nothing, so a second click cannot start a second operation. It reports
// whether it started one.
func (a *app) async(op string, work func() error, done func(error)) bool {
	if a.busy[op] {
		return false
	}
	if a.busy == nil {
		a.busy = map[string]bool{}
	}
	a.busy[op] = true
	go func() {
		err := work()
		a.update(func() {
			delete(a.busy, op)
			done(err)
		})
	}()
	return true
}

// removeDialog is the Remove host confirmation. err is the last failed try,
// which keeps the dialog open.
type removeDialog struct {
	address, name string
	err           string
}

func removeKey(address string) string { return "remove|" + address }

// openRemove opens the confirmation for removing host.
func (a *app) openRemove(host state.Host) {
	d := &removeDialog{address: host.Address, name: host.Name}
	a.openDialog(removeKey(host.Address), host.Address, "Remove "+host.Name+"?", 200, func(c *ui.Context) {
		a.removeView(c, d)
	})
}

// removeView draws the Remove host confirmation. Yes is disabled while the host
// is being removed, so a second click removes nothing more.
func (a *app) removeView(c *ui.Context, d *removeDialog) {
	theme := c.Theme()
	ui.Text(c, "This removes the host and closes its connections.").TextColor(theme.TextMuted)
	if d.err != "" {
		ui.Text(c, d.err).TextColor(theme.Danger)
	}
	busy := a.busy[hostRemoveOp(d.address)]
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "No").Disabled(busy).Clicked() {
			a.closeDialog(removeKey(d.address))
		}
		yes := "Yes"
		if busy {
			yes = "Removing…"
		}
		if ui.PrimaryButton(c, yes).Disabled(busy).Clicked() {
			d.err = ""
			a.removeHost(d.address, func(err error) {
				if err != nil {
					d.err = err.Error()
				}
			})
		}
	})
}
