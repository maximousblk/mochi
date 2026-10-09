# WebDAV

The WebDAV launcher mounts a Mochi host's WebDAV service. Requests are WebDAV
end to end: this machine proxies each request over Tailcat, and the host answers
it.

## Server side

With the WebDAV switch on, the server serves its home volume as WebDAV on port
8080. The embedded WebDAV server keeps its own lock table. The switch is off by
default.

Only Mochi hosts serve WebDAV.

## Launcher

Enabling a host's WebDAV launcher starts a loopback listener under a fresh
secret path. The address is copied to the clipboard, and mounting works as in
[Files](FILES.md#launcher). Turning the launcher off closes the listener.

The request path carries the share's secret path in an `X-Mochi-Share-Prefix`
header. The host accepts only requests under that path, so only the address Mochi
displayed reaches the share.

## Logs

Errors are written to `webdav.log` beside `mochi.db`. See [State](STATE.md#logs).
