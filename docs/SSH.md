# SSH

The SSH launcher reaches a Mochi host's SSH server through Tailcat, so `ssh`,
`scp` and `sftp` work with the host as they do with any SSH server. Mochi does
not change your SSH config.

## Server side

With the SSH switch on, Mochi runs an SSH server in process, on Tailcat port
22022. The port is not 22, so it does not clash with an OpenSSH server on the
same machine. The switch is off by default.

The host key is derived from the machine's identity, so it stays the same for as
long as the identity does, and no key file is written.

On macOS, shells reach the same files as a terminal with **Full Disk Access**
only if Mochi has it, because macOS checks the app that started them. See
[WebDAV](WEBDAV.md#server-side): the SSH tile is yellow while it is missing,
and Mochi asks for it once per run, for SSH and WebDAV together.

### Accounts

The SSH username selects the account. A username that names the account Mochi
runs as gets a session. Any other username is refused at authentication. There
are no SSH passwords or keys: the Tailcat tunnel has already checked the client
(see [Admission](ADMISSION.md)).

A session is one of these:

- a login shell, when the client asks for none (PowerShell on Windows);
- the command the client gave, run through the account's shell;
- with a PTY, when the client asks for one (ConPTY on Windows);
- the `sftp` subsystem, which serves the whole filesystem with the account's
  home directory as the working directory. `scp` and `sftp` both use it.

Mochi runs as one account and does not switch to another, which would need root.
Port forwarding and agent forwarding are not offered.

## Client side

Turning on a host's SSH launcher tunnels to the host's SSH server through a
loopback port on this machine, and copies the command that uses it:

```text
ssh -p <port> -o HostKeyAlias=mochi-node-<node key> 127.0.0.1
```

Put your user name on the host before `127.0.0.1`, for example
`ssh -p 40123 -o HostKeyAlias=mochi-node-… alice@127.0.0.1`. For `scp` and
`sftp`, use the same options; they take the port as `-P`.

`HostKeyAlias` names the host by its node key, so OpenSSH's `known_hosts` file
records the host under a name that does not change. The loopback port changes each
time you connect, so the address alone would not identify the host.

Connecting does not check the host first. A host that does not approve this
machine refuses the tunnel, and the `ssh` command then fails to connect.

## Disconnecting

Clicking the SSH launcher again, removing the host, or quitting Mochi closes the
tunnel. Mochi also closes it once six hours pass without data moving through it.
A connection that is still open counts as in use.

## Settings

Right-click the SSH tile and choose **Configure…**. On the **This device** page this
edits this machine's server; on a host's page it edits that host's client.

Server fields (This device page):

- **Tailcat port** (`port`), default `22022`: the port the SSH server serves.

Client fields (host page, saved per host):

- **Host's SSH port** (`port`), default `22022`: the port the tunnel dials on
  the host.
- **User name** (`user`), default empty: the account put before `127.0.0.1` in
  the copied command. Empty leaves the choice to `ssh`.
