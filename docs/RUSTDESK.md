# RustDesk

The RustDesk launcher lets RustDesk connect to a host by direct IP, through
Tailcat.

## Server side

With the RustDesk switch on, the server forwards TCP port 21118 to RustDesk on
this machine. The switch is off by default. The host must also enable direct IP
access in RustDesk under **Settings → Security**.

## Launcher

Enabling a host's RustDesk launcher does the following:

1. Mochi checks that the host accepts a connection on port 21118. If it does
   not, the launcher stays off and the tooltip says why.
2. Mochi forwards a local port to the host's port 21118.
3. Mochi copies a `127.0.0.1:<port>` address to the clipboard.

Enter that address in RustDesk as the remote ID. Turning the launcher off closes
the forward. Nothing is written to disk.
