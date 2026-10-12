# State

Mochi keeps its state in one SQLite database, `mochi.db`, in the user config
directory:

```text
Linux:   ~/.config/mochi/mochi.db
macOS:   ~/Library/Application Support/mochi/mochi.db
Windows: %AppData%\mochi\mochi.db
```

The database runs in WAL mode. Writers wait on a busy timeout rather than
failing at once.

## What is stored

- **Hosts:** name and Tailcat address.
- **Settings:** the Tailcat server switch and each launcher's server switch.
- **Connections** are not stored. A launcher's connection to a host exists only
  while Mochi runs, and is closed when it has been idle for six hours.
- **Identity:** the machine's Ed25519 seed. Its public half is the machine key
  shown in Settings. See [Admission](ADMISSION.md).
- **Server:** the server's node key, pre-shared key and relay region. They
  persist across restarts, so the server address stays the same.
- **Admission:** trusted CA keys, approved and blocked clients, and the
  certificates pasted into Settings.

## Security

The identity seed is stored unencrypted in `mochi.db`. Anyone who can read the
file can act as this machine. Moving the seed to the platform keychain is
planned, not implemented.

## Logs

Mochi logs to `mochi.log`, beside `mochi.db`, as well as to stderr: server
start failures, tray problems and the like. It is appended to and never
rotated.

WebDAV errors go to `webdav.log`, beside `mochi.db`. Start Mochi with
`--verbose` to log every WebDAV request and its status.

## Upgrades

- Installs from before identities existed had a random client key that cannot
  become a seed. They start a new identity, and hosts that knew the old client
  key must approve the new one.
