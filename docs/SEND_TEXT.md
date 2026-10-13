# Send text

Send text moves plain text between Mochi machines, and from any Tailcat client.
A host with **Send text** switched on in Settings receives text on Tailcat
port 1, saves it, and shows a desktop notification. Clicking the notification
opens the **Inbox**, where each text has a Copy button and a delete button.

## Receiving (server side)

Turn on **Send text** on the **This device** page. Mochi then listens on
port 1 of its Tailcat server. The switch is saved, so it stays on across
restarts. The tile is yellow when the switch is on but the app cannot show
notifications, since texts would go unseen.

The transport is raw bytes, the same as netcat, so the stock Tailcat client
works without Mochi:

```sh
echo hello | tailcat <mochi-server-address>
```

Tailcat's client sends stdin to port 1 by default, so no port is needed.

Each received text:

- is read until the sender closes the connection, within 30 seconds;
- is capped at 64 KiB. A longer text keeps its first 64 KiB, and its
  notification says so;
- has invalid UTF-8 replaced, and empty texts are ignored;
- is saved, keeping only the newest 100 texts.

The notification shows the sender's name (its approved certificate name, else
its node key, else its address), and the start of the text. The full text is in
the Inbox. If the notification cannot be shown, the text is still saved and the
status bar says so.

## Inbox

The **Inbox** section of the **This device** page shows the number of saved texts. The section lists
them newest first, with the sender, the local time, the text, and a Copy button
and a delete button for each. Clicking a Send text notification opens the Inbox.

## Sending (client side)

On a host's page, the **Send text** tile opens a Send text window. It sends the body to
the host's port, which defaults to 1, and reports the outcome. A client must be
approved by the host like any other connection, and a send to a host that has
Send text off is not saved anywhere.

## Settings

Right-click the Send text tile and choose **Configure…**. On the **This device** page
this edits this machine's receiver; on a host's page it edits that host's
client.

Server field (This device page):

- **Tailcat port** (`port`), default `1`: the port the receiver listens on.

Client field (host page, saved per host):

- **Default port** (`port`), default `1`: the port the Send text window fills
  in for that host. The window's Port box can still be changed per send.
