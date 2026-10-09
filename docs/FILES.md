# Files

The Files launcher mounts a host's files on this machine through Windows Explorer
or macOS Finder.

## Server side

With the Files switch on, the server serves this machine's files over SFTP on
port 22, read-write, from the root of its home volume. It is on by default.

## Launcher

Enabling a host's Files launcher does the following:

1. Mochi serves the host's SFTP service through a local WebDAV proxy. The host
   can run a stock `tailcat --files` server or another Mochi.
2. The proxy listens on `127.0.0.1` under an unguessable path.
3. Mochi copies the address to the clipboard.

Mount the address with **Map network drive** in Windows Explorer, or **Connect
to Server** in macOS Finder. Turning the launcher off stops the proxy.

To mount a Mochi host's WebDAV service instead, see [WebDAV](WEBDAV.md).
