# Testing

Tests check what a user can do with Mochi, not how much of each line runs.
Coverage numbers are a diagnostic, not a goal.

## Principles

- **Behavior first.** Each test asserts something a user or another machine
  can observe: a file read through a share, a port that opens or refuses, a
  node that gets in or is turned away.
- **Automation over integration over unit.** Prefer the test that drives the
  real path: the app view, a Tailcat tunnel, a launched process. Use a unit test
  only for logic with no observable path of its own (parsers, the install
  lookup).
- **Shared harness.** Tunnel tests start their network with
  `internal/testharness`: `Relay(t)` starts a DERP relay, `Host(t, relay, ports)`
  starts a Tailcat host serving the ports a test needs, and `Address(host)`
  returns the address clients dial. `HostFunc` does the same with the handler
  looked up on each connection, for a test whose ports are registered while the
  host runs. Don't set up a relay by hand.
- **No test-only exports.** If a test needs internal state, test it from the
  package that owns the state.

## Features and where they are tested

| Feature | Tests |
| --- | --- |
| Approve, block and un-block a node from Settings | `app/ui_test.go`: `TestUIApproveAndBlock`, `TestUIEachButtonActsOnItsOwnRow` |
| Whole session: admin approval, certificates, every launcher and setting | `app/app_e2e_test.go`: `TestApplication` |
| Probation, certificate renewal, rejected and expired nodes | `internal/admission/*_test.go` |
| Server address and approvals survive a restart | `internal/admission/onekey_test.go`: `TestServerRestart` |
| A machine can serve and dial at once | `internal/admission/onekey_test.go`: `TestServeAndDialOnOneMachine` |
| Issue a certificate from the command line | `cmd/issue_test.go` |
| A tunnel carries bytes both ways, reports when it was last used, and closes on disconnect | `internal/connector/tunnel_test.go` |
| A port is served only between Listen and Stop | `internal/connector/ports_test.go`, `internal/connector/webdav/webdav_test.go`, `internal/connector/ssh/connector_test.go`, `internal/connector/rustdesk/listen_test.go` |
| SSH: the copied command reaches the host's SSH server as the account the user names | `internal/connector/ssh/connector_test.go`: `TestSSHOverTailcatThroughCopiedCommand` |
| Removing a host disconnects its clients before deleting it | `app/connections_test.go` |
| Clients idle for six hours are disconnected | `app/connections_test.go` |
| WebDAV share reads, lists, writes and moves through a tunnel | `internal/connector/webdav/webdav_test.go` |
| Share addresses and request logs | `internal/share/share_test.go` |
| RustDesk tunnels direct IP, launches the client, refuses when direct IP is off, and disconnects when the launch fails | `internal/connector/rustdesk/connect_test.go` |
| RustDesk install location | `internal/connector/rustdesk/executable_test.go` |
| RustDesk server side is healthy only while direct IP is on | `internal/connector/rustdesk/listen_test.go` |
| A listening but unhealthy launcher tile stays on and is yellow | `app/health_test.go` |
| Send text delivers the exact bytes to a host port | `internal/connector/textsender/send_test.go` |
| Persistent state: hosts, settings, identity, migrations | `internal/state/state_test.go` |

## Running

```sh
TS_DEBUG_ALWAYS_USE_DERP=1 go test ./...
```

The tunnel tests set the DERP-only switch themselves. The full run takes a few
minutes, mostly in the admission tests.

## Adding a feature test

1. Name the user-visible behavior, then find the feature in the table above.
2. Start from the harness: `relay := testharness.Relay(t)`, then `Host` with the
   ports the feature needs.
3. Drive the feature through its public entry point (`Connect`, `Listen`, the
   view, the CLI), and assert what the other side observes.
4. Add a row to the table.
