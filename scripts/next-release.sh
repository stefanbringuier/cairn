#!/bin/sh
# Print the next stable tag; a tagged HEAD needs no second release on rerun.
set -eu

if git tag --points-at HEAD --list 'v[0-9]*' | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  exit 0
fi

svu_bin=${1:-svu}
next=$("$svu_bin" next --always)
if ! printf '%s\n' "$next" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf 'Invalid release version: %s\n' "$next" >&2
  exit 1
fi
printf '%s\n' "$next"
