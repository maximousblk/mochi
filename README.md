# Mochi

Mochi is a native desktop proof of concept built around the Tailcat Go library.
It has no web frontend, JavaScript runtime, or Tailcat CLI dependency.

The current POC can:

- run as a native MyGo window;
- start in the background with a tray icon;
- open the window from the tray and keep the process alive when it closes;
- use a compact custom title bar with native platform window controls;
- embed Ioskeley Mono as the app's UI typeface;
- show application status in a dedicated bottom bar and highlight enabled SSH launchers;
- add and persist Tailcat host addresses;
- keep application state in a SQLite database;
- apply the static embedded `bootstrap.json` settings at startup;
- add or remove marked Mochi-managed SSH config blocks per host;
- start and stop an embedded Tailcat server;
- expose Tailcat's built-in SSH service on port 22;
- print the server address in the UI.
- send typed text to a host's TCP port (default 1, as the tailcat CLI and browser demo use) from the **Send text** launcher.
- share a host's files from the **Files** launcher. The host serves SFTP (a stock `tailcat --files`, or another Mochi). Mochi serves that as WebDAV on `127.0.0.1` at an unguessable path until you toggle it off, and copies the address to the clipboard. Mount it with Windows Explorer's **Map network drive** or macOS Finder's **Connect to Server**.
- mount a Mochi host's WebDAV service from the **WebDAV** launcher. The host runs an embedded WebDAV server, and this machine proxies each request over Tailcat, so the requests are WebDAV end to end. Only Mochi hosts serve WebDAV. The address is copied and opened the same way.
- WebDAV errors are written to `webdav.log` beside `mochi.db`. Start Mochi with `--verbose` to also log every request and its status. The share token is written as `<share>`.
- forward a host's RustDesk direct-IP port (TCP 21118) from the **RustDesk** launcher. Enabling checks that the host accepts the connection, then copies a `127.0.0.1:<port>` address to enter in RustDesk. The host must enable direct IP access under Settings → Security.

Ioskeley Mono v2.1.0 is bundled under the SIL Open Font License 1.1; its license
is included in `resources/fonts/OFL.txt`.

This version intentionally has no keychain integration, signed keys, certificate
issuance, terminal implementation, or production security policy yet.

## Prerequisites

The repository provides a Nix development shell. Enter it with:

```sh
nix develop
```

The shell supplies Go 1.27, build tooling, and Linux graphics dependencies used
by MyGo's native UI. macOS and Windows use their native graphics APIs.

Without Nix, install Go 1.27 or newer and the native UI dependencies for your
platform.

## Run

```sh
go run .
```

The first build downloads Go modules, including MyGo and Tailcat. The app starts
in the background with a tray icon. Use **Open Mochi** from the tray menu to
show the frameless native window, or double-click the tray icon. Closing the
window hides it and leaves Mochi running in the background. Use **Exit** in the
tray menu to quit.

Use **+ Add host** at the top of the sidebar, then enter a name and complete
`tc...` Tailcat address. Hosts are listed in the scrollable sidebar and stored
in the SQLite state database at the platform's user config directory:

```text
Linux:   ~/.config/mochi/mochi.db
macOS:   ~/Library/Application Support/mochi/mochi.db
Windows: %AppData%\mochi\mochi.db
```

The **Enable Tailcat server** setting is off by default and persists in
SQLite. Changing it starts or stops the server; the control is disabled while
that operation is in progress. The server keeps its key, pre-shared key and relay
region between runs, so its address stays the same. Port 22 is handled by the
embedded built-in SSH service.

### Who may connect

Each machine has one secret, an Ed25519 seed in the SQLite state. Its public half
(**Machine key** in Settings, 64 hex characters) is the machine's identity. The
tailcat node keys it serves and dials with are derived from the seed, separately
for each role, because one node key cannot serve and dial at once without losing
traffic through the relay. Installs from before this change had a random client
key that cannot become a seed: they start a new identity, and hosts that knew the
old client key must approve the new one.

A node may use the server only while a trusted machine key has approved it.
The trusted keys are this machine's own, and those listed in the embedded
`bootstrap.json`:

```json
"trusted_keys": [{ "name": "Acme CA", "key": "<64 lowercase hex chars>" }]
```

A malformed or duplicate key stops Mochi at startup. Changing the list needs a new
build; a node approved by a key that has been removed loses that approval at the
next start.

- A node the server has not seen gets one of 32 probation slots for 20 seconds and
  can reach only the proof port. Anyone who knows the server's public key can take
  slots, with or without the pre-shared key, so a flood can delay first contact of
  new nodes. Nodes already approved are unaffected.
- A client presents certificates there (pasted under **Certificates** in Settings,
  made with `mochi issue --node nodekey:... --id alice --ttl 12h --out alice.cert`
  on a machine whose key a host trusts). A valid one approves the node until it
  expires; later connections need no certificate. `issue` signs with this machine's
  own key unless `--key file` names a file holding a CA's private key as hex (a
  32-byte seed or a 64-byte key). When a host refuses, it lists the CAs it trusts;
  a client whose certificates match none of them says so and names those CAs.
- Under **Pending approval** in Settings, **Approve** signs a 24 hour certificate
  with this machine's own key, and the host renews it, so it lasts until **Block**.
- Settings has a **Connectors** row with one switch per connector: SSH, Files,
  WebDAV and RustDesk. Each switch sets what this machine offers other machines while
  the server runs. SSH serves shell sessions on port 22. Files serves this machine's
  files over SFTP on port 22, read-write from the root of its home volume. WebDAV
  serves the same root as WebDAV on port 8080, from an embedded WebDAV server that
  holds its own locks. RustDesk forwards port 21118 to RustDesk on this machine, which
  needs direct IP access enabled. SSH and Files are on by default; WebDAV and RustDesk
  are off. Switches are saved in SQLite and take effect without restarting the server.
  The WebDAV switch used to control the Files service; on upgrade its stored value
  moves to Files and the WebDAV switch starts off.
- An approved node may use every service the server offers. Per-service policy beyond
  these switches is not implemented.

Each host has an **SSH launcher**. Connectors implement the shared `Connector`
interface, including `Enable` and idempotent `Disable` operations. Removing a
host calls every connector's `Disable` before deleting its SQLite row; all
connectors are attempted, and Mochi keeps the host if any cleanup fails so it
can be retried. The SSH connector manages a marked block in the user's native
SSH config and preserves unrelated entries. The block invokes the current
Mochi executable by absolute path and runs `connector-ssh-proxy` headlessly.
The command is `mochi connector-ssh-proxy <tailcat-address> <port>`; it relays
stdin/stdout directly to the host's Tailcat TCP port and writes diagnostics only
to stderr. Mochi writes the exact stored Tailcat address into `ProxyCommand`,
and also restores its case from SQLite for older managed entries because
Windows OpenSSH lowercases `%h`. It uses the client node key derived from the
identity stored in the SQLite state database and presents the stored certificates
to Mochi hosts. A host that does not approve it fails the connection with the
reason. The seed is plain in the POC SQLite file; production should move it to
the platform keychain.

`bootstrap.json` is embedded as a static startup input. Mochi applies its
settings to SQLite at launch, then uses SQLite for the rest of the session. It
does not read or modify the bootstrap file during normal operation.

## Checks

```sh
gofmt -w .
go test ./...
go build .
```

To run the native MyGo development build when the CLI is available:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.2.15 dev
```

## Cross-build

Mochi is intended to be cross-buildable as a Go application:

```sh
go run github.com/egoist/mygo/cmd/mygo@v0.2.15 build \
  -platform windows/amd64,linux/amd64,linux/arm64
```

Native packaging, signing, and platform graphics dependencies are separate from
this initial POC. Do not treat the generated binaries as production releases.

## License

Mochi is released under the MIT License; see `LICENSE`. Ioskeley Mono is under
the SIL Open Font License 1.1 (`resources/fonts/OFL.txt`).
