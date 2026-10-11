#!/bin/sh
set -eu

fail() { printf 'cairn: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Linux ] || fail 'This installer supports Linux.'
case "$(uname -m)" in
  x86_64 | amd64) cairn_arch=amd64 ;;
  aarch64 | arm64) cairn_arch=arm64 ;;
  *) fail 'Release binaries support amd64 and arm64.' ;;
esac
for cairn_tool in curl tar sha256sum awk install; do
  command -v "$cairn_tool" >/dev/null 2>&1 || fail "Install $cairn_tool first."
done

cairn_version=${CAIRN_VERSION:-latest}
cairn_url=https://github.com/stefanbringuier/cairn/releases
if [ "$cairn_version" = latest ]; then
  cairn_url=$cairn_url/latest/download
else
  printf '%s\n' "$cairn_version" | awk '/^v[0-9]+\.[0-9]+\.[0-9]+$/ {valid=1} END {exit !valid}' || fail 'CAIRN_VERSION must be latest or vX.Y.Z.'
  cairn_url=$cairn_url/download/$cairn_version
fi
cairn_dir=${CAIRN_INSTALL_DIR:-"$HOME/.local/bin"}
cairn_archive=cairn_linux_$cairn_arch.tar.gz
cairn_tmp=$(mktemp -d)
trap 'rm -rf "$cairn_tmp"' 0
trap 'exit 1' 1 2 3 15

printf 'Installing cairn · Linux %s\n' "$cairn_arch"
curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 180 \
  "$cairn_url/$cairn_archive" -o "$cairn_tmp/$cairn_archive"
curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 180 \
  "$cairn_url/checksums.txt" -o "$cairn_tmp/checksums.txt"
awk -v archive="$cairn_archive" '$2 == archive {print; found=1} END {exit !found}' \
  "$cairn_tmp/checksums.txt" > "$cairn_tmp/verify.txt" || fail 'Release checksum is missing.'
(cd "$cairn_tmp" && sha256sum --check --status verify.txt) || fail 'Release checksum did not match.'
tar -xzf "$cairn_tmp/$cairn_archive" -C "$cairn_tmp" cairn
[ -f "$cairn_tmp/cairn" ] && [ ! -L "$cairn_tmp/cairn" ] || fail 'Release is missing the cairn executable.'
mkdir -p "$cairn_dir"
install -m 755 "$cairn_tmp/cairn" "$cairn_dir/cairn"

printf '\nInstalled %s/cairn\n' "$cairn_dir"
case ":${PATH:-}:" in
  *":$cairn_dir:"*) ;;
  *) printf 'Add %s to your PATH before running cairn.\n' "$cairn_dir" ;;
esac
printf '\nA way back to your files.\n\n  cairn init /mnt/backup/cairn\n  cairn\n'
