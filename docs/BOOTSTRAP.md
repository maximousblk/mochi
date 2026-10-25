# Bootstrap

`bootstrap.json` is embedded in the build and supplies the static trust anchors used by admission. It is not mutable application state and is not written to PocketBase.

```json
{
  "trusted_keys": [
    { "name": "Acme CA", "key": "<64 lowercase hex characters>" }
  ]
}
```

- `trusted_keys`: machine keys whose certificates this build accepts. See [Admission](ADMISSION.md#trusted-keys).

A malformed or duplicate key stops Mochi at startup. Changing the list needs a new build.
