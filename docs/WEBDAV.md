# WebDAV

The WebDAV launcher mounts a Mochi host's files on this machine through Windows
Explorer or macOS Finder. Requests are WebDAV end to end: the tunnel carries each
request to the host, and the host answers it.

## Server side

With the WebDAV switch on, this machine serves the root of its home volume as
WebDAV on Tailcat port 8080, read-write, with its own lock table. That is the
same reach a shell session has. The switch is off by default.

On macOS, Mochi needs **Full Disk Access** to serve protected folders such as
Desktop, Documents, Downloads, iCloud Drive and other volumes; without it Finder
fails to list them. It covers all of them, so no folder asks on its own. macOS
has no prompt for it, and no Info.plist key requests it. While the switch is on
and Mochi lacks it, the WebDAV tile in Settings is yellow, and the first time
Mochi notices, it opens
**System Settings → Privacy & Security → Full Disk Access** for you to turn
Mochi on there. It asks once per run, for SSH and WebDAV together. The tile
returns to normal within a few seconds of granting it.
The grant lasts across installs only if each build is signed with the same
identity; see [Install](INSTALL.md#platform-notes).

Only Mochi hosts serve WebDAV.

## Launcher

Turning on a host's WebDAV launcher does the following:

1. Mochi tunnels to the host's port 8080 through a loopback port on this machine.
   It first checks that the host serves WebDAV. If the host does not, the
   launcher stays off and the status says why.
2. Mochi opens the share in Explorer (Windows) or Finder (macOS). Other systems
   have no file manager to open, so they only get the address.
3. Mochi copies the address: `\\127.0.0.1@<port>\DavWWWRoot` on Windows, and
   `http://127.0.0.1:<port>/` elsewhere.

Clicking the WebDAV launcher again, removing the host, or quitting Mochi closes
the tunnel, and the share stops working. So does six hours without data moving
through the tunnel.

## Access

The share needs no credential of its own. The tunnel is the access check: a
host admits only the machines it approves (see [Admission](ADMISSION.md)), and
it refuses the others before any request reaches the share.

## Logs

Errors are written to `webdav.log` beside `mochi.db`. Start Mochi with
`--verbose` to log every request and its status. See [State](STATE.md#logs).
