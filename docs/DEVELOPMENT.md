# Development

## Setup

```sh
nix develop
```

The shell provides Go 1.27, `gofmt`, `golangci-lint` and the Linux graphics
libraries that MyGo needs.

## Run

```sh
go run .                                                        # run the app
go run github.com/egoist/mygo/cmd/mygo@v0.2.15 dev              # MyGo development build
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

## Code map

| File | Purpose |
| --- | --- |
| `main.go` | App lifecycle, tray, window and the main view |
| `db.go`, `region.go` | SQLite state: hosts, settings, identity, certificates |
| `identity.go` | The machine's Ed25519 seed and the node keys derived from it |
| `gate.go`, `cert.go`, `issue.go`, `dial.go` | Admission: probation, approvals, certificates |
| `connectors.go` | The `Connector` interface shared by the launchers |
| `sshconnector.go`, `sshconfig.go`, `sshproxy_test.go` | SSH launcher and `connector-ssh-proxy` |
| `webdav.go`, `webdav_share.go`, `webdavfs.go` | WebDAV service and launcher |
| `files.go` | Files service (SFTP) and launcher |
| `rustdesk.go` | RustDesk port forward |
| `textsender.go` | Send text launcher |
| `bootstrap.json` | Embedded startup configuration ([Bootstrap](BOOTSTRAP.md)) |

## Conventions

- Use `nix develop --command ...` when a tool is not on your path.
- Comments explain behavior that code cannot show. Don't describe history.
