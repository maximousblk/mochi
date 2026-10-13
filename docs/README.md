# Mochi

Native desktop app for Tailcat hosts. **Status: alpha.** Expect breaking changes
and rough edges.

## Features

- Native window with a compact title bar and a status bar.
- Tray icon: double-click it, or choose **Open Mochi**, to show the window.
  Closing the window keeps Mochi running.
- Host list: add, remove and persist Tailcat addresses.
- Embedded Tailcat server, with a per-machine identity and admission control.
- Approve clients with certificates, or from the pending list.
- Per-host launchers:
  - **SSH**: a tunnel to the host's SSH server, and the `ssh` command for it ([SSH](SSH.md)).
  - **WebDAV**: mount a Mochi host's files in Explorer or Finder ([WebDAV](WEBDAV.md)).
  - **Send text**: send text to a host's Inbox, and receive it from stock `tailcat` ([Send text](SEND_TEXT.md)).
  - **RustDesk**: forward a host's RustDesk direct-IP port ([RustDesk](RUSTDESK.md)).
- Startup settings from an embedded `bootstrap.json` ([Bootstrap](BOOTSTRAP.md)).

## Quick start

With Nix, from a checkout:

```sh
nix run .
```

From source, in the development shell:

```sh
nix develop
go run .
```

Then:

1. Click **+ Add host** and enter a name and a `tc...` Tailcat address.
2. To share this machine, turn on **Enable Tailcat server** in Settings.

## Documentation

- [Install](INSTALL.md): supported ways to get Mochi.
- [Development](DEVELOPMENT.md): checks, builds and the code layout.
- [Admission](ADMISSION.md): who may connect to this machine.
- [State](STATE.md): where data lives, and what is stored.
- [Bootstrap](BOOTSTRAP.md): the embedded startup configuration.
- [SSH](SSH.md), [WebDAV](WEBDAV.md), [RustDesk](RUSTDESK.md): launchers and the services they use.

## Known limits

- The machine's identity seed is stored unencrypted in the SQLite state.
- No keychain integration, signed releases or installers yet.
- An approved client may use every service the server offers.

## Launcher settings

Right-click any launcher tile and choose **Configure…** to edit that launcher's
settings. On the Settings page it edits this machine's server; on a host's page
it edits that host's client. Each launcher's doc lists its fields.

## License

Mochi is released under the MIT License; see [`LICENSE`](../LICENSE). Ioskeley
Mono is under the SIL Open Font License 1.1 (`resources/fonts/OFL.txt`).
