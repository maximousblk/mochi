# Development

## Setup

```sh
nix develop
```

The shell provides Go 1.27, `gofmt`, `golangci-lint` and the Linux graphics
libraries that MyGo needs.

## Run

```sh
go run .            # the app
go run . issue ...  # the certificate command, see ADMISSION.md
```

## Checks

```sh
gofmt -l .
go vet ./...
go test ./...
go build .
```

The test suite takes a few minutes. It includes end-to-end tests that run a
local DERP relay, so it needs loopback networking.

## Builds

- Static Windows build: see [Install](INSTALL.md#static-binaries).
- Nix package: `nix build .#mochi`. Its `vendorHash` must change when
  `go.mod` or `go.sum` change; Nix prints the new value on mismatch.
- Release builds use `build-tags.txt`, a copy of the pinned Tailcat's list of
  tailscale.com features to leave out, plus `mygo_noinspector`. After a Tailcat
  bump, copy its `build-tags.txt` again; `build_tags_test.go` fails until you
  do. Plain `go build` without the tags works too, only larger.

## Code map

The root `main.go` only dispatches: `issue` goes to `cmd/`, and everything else
starts the window in `app/`.

| Path | Purpose |
| --- | --- |
| `main.go` | Dispatch between the CLI subcommands and the app |
| `cmd/` | CLI subcommands: `Issue` |
| `app/` | The window: lifecycle, screens, tray, Send-text window, approvals list, embedded icons and fonts |
| `internal/state/` | SQLite state, identity seed, bootstrap trust list, state paths |
| `internal/admission/` | Who may connect: gate, probation, certificates, dialing |
| `internal/server/` | Relay region selection for the embedded Tailcat server |
| `internal/sshserver/` | The in-process SSH server: sessions, PTY, sftp subsystem |
| `internal/share/` | WebDAV helpers: file manager hand-off, request logging |
| `internal/connector/` | The `Connector` and `Client` interfaces, the Tailcat port registry, and the client tunnel |
| `internal/connector/ssh/` | SSH launcher: serves `sshserver` on port 22022, and tunnels to a host's SSH |
| `internal/connector/webdav/` | WebDAV launcher: serves the home volume, and mounts a host's share through a tunnel |
| `internal/connector/rustdesk/` | RustDesk launcher: direct-IP forward, and the RustDesk client on a tunnel |
| `internal/connector/textsender/` | Send text: the sender, and the receiver on port 1 that saves texts |
| `mygo.json` | MyGo app metadata; its icon is `app/resources/icon.png` |

## Conventions

- Use `nix develop --command ...` when a tool is not on your path.
- Comments explain behavior that code cannot show. Don't describe history.
- The UI thread owns app state. Anything that can block (a network dial, a store read or write, launching a program, opening a path) runs in a goroutine, and its result is applied with `a.update`, which also redraws every open window. An operation a button starts goes through `a.async` under a busy key, so the control shows it and stays disabled until it finishes.
- Each dialog is its own window, opened through `openDialog` so one window exists per dialog target. Dialog state lives in that dialog, never in a shared app field.
