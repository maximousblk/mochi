# Install

Mochi is alpha. These are the only supported ways to get it today:

1. **Static binaries**: standalone executables built with MyGo.
2. **Nix flake app**: run or build the flake's package.
3. **Source build**: build with Go from a checkout.

There are no installers, package-manager packages or signed releases yet.

## Static binaries

Build a Windows executable from a checkout:

```sh
nix develop --command bash -lc \
  'GOFLAGS="-tags=$(cat build-tags.txt)" go run github.com/egoist/mygo/cmd/mygo build -platform windows/amd64'
```

The result is `dist/windows-amd64/Mochi.exe`. Windows amd64 is the platform
that has been run. Other targets, such as `windows/arm64`, are built the same
way with `-platform`.

## Nix flake app

The flake has `Mochi` and `MochiInstaller` for six systems:
`x86_64-linux`, `aarch64-linux`, `x86_64-darwin`, `aarch64-darwin`,
`x86_64-windows` and `aarch64-windows`. The Windows ones are cross-built from
Linux, so `nix build .#packages.x86_64-windows.MochiInstaller` works on Linux.
The macOS `MochiInstaller` (a disk image) needs macOS's `hdiutil`, so it fails
on Linux with that reason. `Mochi.app` for macOS is built on Linux and is
unsigned; sign it on a Mac.

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
