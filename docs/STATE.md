# State

Mochi uses PocketBase as an embedded, headless Go library. It never serves PocketBase's HTTP API. PocketBase stores its databases in the per-user data directory:

```text
Linux:   ~/.config/mochi/data/
macOS:   ~/Library/Application Support/mochi/data/
Windows: %AppData%\mochi\data\
```

The directory holds an embedded PocketBase: `data.db`, `auxiliary.db` and PocketBase's `.notify` directory, which keeps two Mochi processes on one directory (the app and `mochi issue`) in step. PocketBase is never served; Mochi uses its record API. Mochi creates its collections when they are missing (`internal/state/schema.go`); there are no versioned migrations, so a schema change edits that file and existing data is moved by hand. Hosts, launcher settings, machine identity and server parameters, certificates, received texts, and admission clients are PocketBase records. Host deletion cascades to its launcher settings. Received texts are pruned to the newest 100 records. PocketBase's own log table is turned off; Mochi logs to `mochi.log`.

The machine identity seed and server pre-shared key are hidden record fields, but are stored unencrypted. Anyone who can read the data directory can act as this machine. Moving secrets to the platform keychain is not implemented.

## Logs

`mochi.log` and `webdav.log` remain in the per-user Mochi directory (the parent of `data/`). Mochi appends logs and does not rotate them. The Settings page shows the PocketBase data directory.

## Upgrades

Mochi does not import the former `mochi.db` or legacy `config.json`. Existing data must be migrated by hand outside the app. Installs from before identities existed had a random client key that cannot become a seed; hosts that knew the old client key must approve the new one.
