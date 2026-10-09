# Bootstrap

`bootstrap.json` is embedded in the build as a static startup input. Mochi
applies its settings to the state database at launch and uses the database for
the rest of the session. It does not read or modify the file during normal
operation.

```json
{
  "settings": {
    "ssh_config_path": ""
  },
  "trusted_keys": [
    { "name": "Acme CA", "key": "<64 lowercase hex characters>" }
  ]
}
```

- `ssh_config_path`: the SSH config file the SSH launcher manages. An empty
  value means `~/.ssh/config`.
- `trusted_keys`: machine keys whose certificates this build accepts. See
  [Admission](ADMISSION.md#trusted-keys).

A malformed or duplicate key stops Mochi at startup. Changing the list needs a
new build.
