# State

Mochi keeps its state in one SQLite database, `mochi.db`, in the user config
directory:

```text
Linux:   ~/.config/mochi/mochi.db
macOS:   ~/Library/Application Support/mochi/mochi.db
Windows: %AppData%\mochi\mochi.db
```

The database runs in WAL mode. Every process that opens it uses a busy timeout,
so the app and `connector-ssh-proxy` can share it.

## What is stored

- **Hosts:** name and Tailcat address.
- **Settings:** the Tailcat server switch, each connector's server switch and
  the SSH config path.
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

WebDAV errors go to `webdav.log`, beside `mochi.db`. Start Mochi with
`--verbose` to log every WebDAV request and its status. The share token is
written as `<share>`.

## Upgrades

- Installs from before identities existed had a random client key that cannot
  become a seed. They start a new identity, and hosts that knew the old client
  key must approve the new one.
- The old WebDAV server switch controlled the Files service. On upgrade, its
  stored value moves to the Files switch, and the WebDAV switch starts off.
