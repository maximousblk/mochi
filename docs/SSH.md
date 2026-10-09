# SSH

The SSH launcher adds a host to your SSH config, so `ssh <name>` connects to it
over Tailcat.

## Server side

With the SSH switch on, the server serves SSH shell sessions on port 22, through
Tailcat's built-in SSH service. It is on by default.

## Launcher

Each host has an SSH launcher. It writes a marked block to your native SSH
config, and other entries in that file are left alone. The block has this shape:

```text
# BEGIN MOCHI MANAGED <id>
Host <name>
    HostName <tailcat-address>
    ProxyCommand "<path to mochi>" connector-ssh-proxy <tailcat-address> %p
# END MOCHI MANAGED <id>
```

`<id>` is the first 16 hex characters of the SHA-256 of the Tailcat address.
The block runs the current Mochi executable by its absolute path, and `%p` is
the port OpenSSH asks for.
`connector-ssh-proxy` runs headlessly. It relays stdin and stdout to the host's
Tailcat TCP port and writes diagnostics only to stderr.

## Why the address is restored

Windows OpenSSH lowercases `%h` before it runs `ProxyCommand`. Mochi writes the
exact stored Tailcat address into the block. For older managed entries, it
restores the address's case from the state database.

## Authentication

`connector-ssh-proxy` uses the client node key derived from the machine's
identity, and presents the stored certificates to the host. A host that does not
approve the client fails the connection and states the reason. See
[Admission](ADMISSION.md).

## Removing a host

Removing a host disables its launchers first, which removes its SSH block. If
any cleanup fails, Mochi keeps the host so that cleanup can be retried.
