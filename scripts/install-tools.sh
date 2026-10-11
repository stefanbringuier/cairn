#!/bin/sh
set -eu

# Match the version pinned in .github/workflows/ci.yml.
version=2.14.0
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
dest="$root/.cache/bin"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) printf '%s\n' 'Install golangci-lint manually on this platform.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf '%s\n' 'Unsupported architecture for the development tools.' >&2; exit 1 ;;
esac

if [ -x "$dest/golangci-lint" ] && "$dest/golangci-lint" version | grep -q "version $version "; then
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
archive="golangci-lint-${version}-${os}-${arch}.tar.gz"
base="https://github.com/golangci/golangci-lint/releases/download/v$version"
curl --fail --silent --show-error --location "$base/$archive" -o "$tmp/$archive"
curl --fail --silent --show-error --location "$base/golangci-lint-${version}-checksums.txt" -o "$tmp/checksums.txt"
checksum=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")
[ -n "$checksum" ] || { printf '%s\n' 'Release checksum missing.' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive")
else
  actual=$(shasum -a 256 "$tmp/$archive")
fi
[ "${actual%% *}" = "$checksum" ] || { printf '%s\n' 'Release checksum mismatch.' >&2; exit 1; }
tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$dest"
install -m 755 "$tmp/golangci-lint-${version}-${os}-${arch}/golangci-lint" "$dest/golangci-lint"
"$dest/golangci-lint" version
