#!/bin/sh
# Signs Mochi.app with a stable self-signed identity, so macOS keeps the
# permissions granted to Mochi (Full Disk Access) when the app is replaced by a
# newer build. Run it on the Mac after each install:
#
#   scripts/sign-macos.sh [path/to/Mochi.app]
#
# The first run creates the identity "Mochi Local Signing" in the login
# keychain. Set KEYCHAIN_PASSWORD (the login password) to run it without a
# keychain prompt, as over SSH.
set -eu

app=${1:-"$HOME/Desktop/Mochi.app"}
identity="Mochi Local Signing"
keychain="$HOME/Library/Keychains/login.keychain-db"

if [ -n "${KEYCHAIN_PASSWORD:-}" ]; then
	security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$keychain"
fi

if ! security find-certificate -c "$identity" "$keychain" >/dev/null 2>&1; then
	dir=$(mktemp -d)
	trap 'rm -rf "$dir"' EXIT
	cat >"$dir/cert.cnf" <<EOF
[req]
distinguished_name = dn
prompt = no
x509_extensions = ext
[dn]
CN = $identity
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF
	openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -config "$dir/cert.cnf" \
		-keyout "$dir/key.pem" -out "$dir/cert.pem" 2>/dev/null
	openssl pkcs12 -export -inkey "$dir/key.pem" -in "$dir/cert.pem" \
		-name "$identity" -passout pass:mochi -out "$dir/identity.p12"
	security import "$dir/identity.p12" -k "$keychain" -P mochi -T /usr/bin/codesign
	if [ -n "${KEYCHAIN_PASSWORD:-}" ]; then
		security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$keychain" >/dev/null
	fi
fi

# A build copied from the Nix store is read-only.
chmod -R u+w "$app"
codesign --force --deep --sign "$identity" "$app"
codesign --display --requirements - "$app" 2>&1 | grep designated
