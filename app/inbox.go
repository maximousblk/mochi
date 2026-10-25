package app

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/maximousblk/mochi/internal/admission"
	"github.com/maximousblk/mochi/internal/connector/textsender"
	"tailscale.com/types/key"
)

// textNotificationPrefix starts the ID of every notification for a received
// text. The ID is the prefix and the text's row ID, so a click can find it.
const textNotificationPrefix = "mochi-text:"

// notificationBodyRunes caps how much of a text the notification shows. The
// Inbox has the rest.
const notificationBodyRunes = 240

// canNotify reports whether the app can show a notification. A test seam
// counts as able to notify, so the receiver is healthy under test.
func (a *app) canNotify() bool {
	return a.notify != nil || mygo.NotificationsSupported()
}

// reloadInbox reads the saved texts off the UI thread and replaces a.inbox when
// the read finishes. A request made during a read is not dropped: another read
// follows, so a text that arrives meanwhile is shown. It runs on the UI thread.
func (a *app) reloadInbox() {
	if !a.inboxReload.start() {
		return
	}
	go func() {
		texts, err := a.store.ReceivedTexts()
		a.update(func() {
			if err != nil {
				a.say(page{}, fmt.Sprintf("Loading texts failed: %v", err))
			} else {
				a.inbox = texts
			}
			if a.inboxReload.done() {
				a.reloadInbox()
			}
		})
	}()
}

// receiveText is called by the receiver for each text, on the connection's
// goroutine. The sender name needs the server and gate, which only the UI
// thread may touch, so the name is resolved there and the rest happens off it.
func (a *app) receiveText(t textsender.Text) {
	a.update(func() {
		sender := a.senderLabel(t.From)
		go a.keepText(sender, t)
	})
}

// keepText saves a received text and tells the user about it. A text is saved
// even when the notification fails, so it is never lost for want of a
// notification.
func (a *app) keepText(sender string, t textsender.Text) {
	at := time.Now()
	id, err := a.store.AddReceivedText(sender, t.Body, at)
	if err != nil {
		a.update(func() { a.say(page{}, fmt.Sprintf("Text from %s not saved: %v", sender, err)) })
		return
	}
	opts := mygo.NotificationOptions{
		ID:    fmt.Sprintf("%s%s", textNotificationPrefix, id),
		Title: "Text from " + sender,
		Body:  notificationBody(t),
		Group: "mochi-text",
	}
	notify := a.notify
	if notify == nil {
		notify = func(o mygo.NotificationOptions) error { return mygo.NewNotification(o).Show() }
	}
	notifyErr := notify(opts)
	a.update(func() {
		a.reloadInbox()
		if notifyErr != nil {
			a.say(page{}, fmt.Sprintf("Text from %s saved, but no notification: %v", sender, notifyErr))
		}
	})
}

// senderLabel names the machine a text came from. The approved name is best,
// then the node key, then the raw address for a connection that has no key.
// It runs on the UI thread.
func (a *app) senderLabel(from net.Addr) string {
	if from == nil {
		return "unknown"
	}
	if a.server != nil {
		if k, ok := a.server.PeerKey(from); ok {
			if name := a.approvedName(k); name != "" {
				return name
			}
			return k.ShortString()
		}
	}
	return from.String()
}

// approvedName returns the name a CA approved k under, or "" if none. A node
// approved from this host's own list has only a placeholder name, so it has none.
func (a *app) approvedName(k key.NodePublic) string {
	if a.gate == nil {
		return ""
	}
	for _, c := range a.gate.Clients(admission.Approved) {
		if c.Key == k && c.Name != "" && c.CA != a.gate.SelfFingerprint() {
			return c.Name
		}
	}
	return ""
}

// notificationBody is the start of a text, with a note when it was cut. A
// long notification would be hard to read, and the Inbox keeps the whole text.
func notificationBody(t textsender.Text) string {
	body := t.Body
	if runes := []rune(body); len(runes) > notificationBodyRunes {
		body = string(runes[:notificationBodyRunes]) + "…"
	}
	if t.Truncated {
		body += "\n(The text was longer than 64 KiB; only the start was kept.)"
	}
	return body
}

// notificationClicked shows the self page, with its Inbox, when a received-text
// notification is clicked. Other notifications are not the app's to handle.
func (a *app) notificationClicked(id string) {
	if !strings.HasPrefix(id, textNotificationPrefix) {
		return
	}
	a.update(func() {
		a.page = page{}
		a.reloadInbox()
		a.showWindow()
	})
}

// deleteText removes one saved text off the UI thread, then reloads the Inbox.
// The delete is keyed by the text, so a second click on the same text does
// nothing while the first is still running.
func (a *app) deleteText(id string) {
	a.async("text-delete|"+id, func() error {
		return a.store.DeleteReceivedText(id)
	}, func(err error) {
		if err != nil {
			a.say(page{}, fmt.Sprintf("Delete failed: %v", err))
			return
		}
		a.reloadInbox()
	})
}

// inboxView is the Inbox section of the self page, newest text first.
func (a *app) inboxView(c *ui.Context) {
	theme := c.Theme()
	ui.Text(c, "Inbox").FontSize(18).Bold()
	if len(a.inbox) == 0 {
		ui.Text(c, "No texts yet.").TextColor(theme.TextMuted)
		ui.Text(c, "To receive texts here, turn on Send text on this page. Other machines can send to this one from their Send text window, or from a terminal with: echo hello | tailcat <this machine's server address>").TextColor(theme.TextMuted)
		return
	}
	for _, t := range a.inbox {
		ui.Column(c).Background(theme.Surface).Padding(12).Gap(6).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, t.Sender).FontWeight(600).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Grow(1)
				ui.Text(c, t.At.Local().Format("2006-01-02 15:04")).TextColor(theme.TextMuted).SingleLine()
				a.copyButton(c, t.Body, "text")
				if iconButton(c, trashIcon, "Delete text").Clicked() {
					a.deleteText(t.ID)
				}
			})
			ui.Text(c, t.Body)
		})
	}
}
