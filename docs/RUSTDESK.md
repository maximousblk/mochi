# RustDesk

The RustDesk launcher lets RustDesk connect to a host by direct IP, through
Tailcat.

## Server side

With the RustDesk switch on, the server forwards TCP port 21118 to RustDesk on
this machine. The switch is off by default.

Mochi does not change RustDesk's settings. Turn on **Enable direct IP access**
in RustDesk under **Settings → Security** yourself. The switch turns on either
way. Every few seconds Mochi probes `127.0.0.1:21118`: while RustDesk does not
accept there, the RustDesk tile in Settings is yellow instead of the accent
color, and connections to this machine fail. It returns to normal once direct
IP access is on. Only the default port is probed, so a custom direct-access
port is not supported.

## Launcher

Turning on a host's RustDesk launcher does the following:

1. Mochi checks that the host accepts a connection on port 21118. If it does
   not, the launcher stays off and the status says why.
2. Mochi tunnels a loopback port on this machine to the host's port 21118.
3. Mochi starts RustDesk with `--connect 127.0.0.1:<port>`, so RustDesk reaches
   the host through the tunnel.

RustDesk must be installed in Program Files on Windows. Otherwise the launcher
says it is not installed, and nothing is opened.

Clicking the RustDesk launcher again, removing the host, or quitting Mochi closes
the tunnel. So does six hours without data moving through it. Nothing is written
to disk.
