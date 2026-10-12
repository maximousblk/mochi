#!/bin/sh
# Builds Mochi's Windows executable with MyGo, then the one-step installer
# with NSIS. Run it from the repository root, in the development shell, which
# provides makensis (flake.nix).
#
#   packaging/windows/build.sh [amd64|arm64]
set -eu

arch="${1:-amd64}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

version="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' mygo.json | head -1)"
if [ -z "$version" ]; then
  echo "build.sh: no version in mygo.json" >&2
  exit 1
fi

go run github.com/egoist/mygo/cmd/mygo build -platform "windows/$arch" -skip-dmg

out="dist/windows-$arch"
exe="$out/Mochi.exe"
if [ ! -f "$exe" ]; then
  echo "build.sh: $exe was not built" >&2
  exit 1
fi

# MyGo writes its own installer next to the executable, with the screens we
# do not want. Ours replaces it under the same name.
installer="$out/Mochi Setup $version $arch.exe"
makensis \
  -DAPP_VERSION="$version" \
  -DAPP_EXE="$root/$exe" \
  -DOUT_FILE="$root/$installer" \
  packaging/windows/installer.nsi

echo "built $installer"
