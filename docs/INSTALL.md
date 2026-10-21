# Install

Mochi is alpha. These are the only supported ways to get it today:

1. **Canary builds**: the latest build of `main`, from GitHub.
2. **Static binaries**: standalone executables built with MyGo.
3. **Nix flake app**: run or build the flake's package.
4. **Source build**: build with Go from a checkout.

There are no package-manager packages or signed releases yet.

## Canary builds

Every push to `main` replaces the
[canary pre-release](https://github.com/maximousblk/mochi/releases/tag/canary)
(`.github/workflows/canary.yml`). It has:

| File | What |
|---|---|
| `Mochi-macos-universal.dmg` | macOS disk image, Apple silicon and Intel |
| `Mochi-Setup-windows-<arch>.exe` | Windows installer, per user, no admin |
| `Mochi-windows-<arch>.exe` | Windows executable, no install |
| `mochi-linux-<arch>` | Linux executable |
| `SHA256SUMS` | checksums of the above |

The apps are unsigned: on macOS open Mochi with right-click → Open the first
time, and on Windows SmartScreen may ask to confirm.

## Static binaries

Build with `mygo build`, the MyGo CLI pinned in `go.mod` as a tool, and
Tailcat's build tags:

```sh
nix develop --command bash -lc \
  'GOFLAGS="-tags=$(cat build-tags.txt)" go tool mygo build -platform windows/amd64'
```

The result is `dist/windows-amd64/Mochi.exe`. Other targets are built the same
way with `-platform`, such as `linux/arm64`, or `darwin/universal` on a Mac for
the disk image. `packaging/windows/build.sh` also builds the one-step installer.

## Nix flake app

The flake has `Mochi` and `MochiInstaller` for six systems:
`x86_64-linux`, `aarch64-linux`, `x86_64-darwin`, `aarch64-darwin`,
`x86_64-windows` and `aarch64-windows`. The Windows ones are cross-built from
Linux, so `nix build .#packages.x86_64-windows.MochiInstaller` works on Linux.
The macOS `MochiInstaller` (a disk image) needs macOS's `hdiutil`, so it fails
on Linux with that reason. `Mochi.app` for macOS is built on Linux and is
unsigned.

From a checkout:

```sh
nix run .            # run Mochi
nix build .#mochi    # build ./result/bin/mochi
```

From another flake, add Mochi as an input and use its package, app or overlay:

```nix
{
  inputs.mochi.url = "github:maximousblk/mochi";

  outputs = { self, nixpkgs, mochi, ... }: {
    # The package for a system:
    #   mochi.packages.x86_64-linux.default
    # Or run it as an app:
    #   nix run github:maximousblk/mochi
    # Or add pkgs.mochi to nixpkgs:
    #   nixpkgs.overlays = [ mochi.overlays.default ];
  };
}
```

## Source build

You need Go 1.27 or newer and cgo. On Linux you also need the GTK 3 and
AppIndicator development libraries. `nix develop` provides all of them.

```sh
go build -o mochi .
./mochi
```

## Platform notes

- **Linux:** the tray uses AppIndicator, which reports no clicks. Use the tray
  menu's **Open Mochi** instead of double-clicking.
- **macOS:** the code builds in the development shell, but no package or app is
  exposed for it yet.
- **Windows:** the tray double-click opens the window.

Mochi stores its data in the user config directory. See [State](STATE.md).
