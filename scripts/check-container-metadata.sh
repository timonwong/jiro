#!/bin/sh
set -eu

output="$(mktemp "${TMPDIR:-/tmp}/jiro-container-metadata.XXXXXX")"
trap 'rm -f "$output"' EXIT INT TERM

./scripts/container-metadata.sh ghcr.io/timonwong/jiro v1.2.3 "$output"
expected_stable='version=1.2.3
tags<<EOF
ghcr.io/timonwong/jiro:v1.2.3
ghcr.io/timonwong/jiro:latest
EOF'
if [ "$(cat "$output")" != "$expected_stable" ]; then
  echo 'stable container metadata is incorrect' >&2
  exit 1
fi

: > "$output"
./scripts/container-metadata.sh ghcr.io/timonwong/jiro v1.2.3-rc.1 "$output"
expected_prerelease='version=1.2.3-rc.1
tags<<EOF
ghcr.io/timonwong/jiro:v1.2.3-rc.1
EOF'
if [ "$(cat "$output")" != "$expected_prerelease" ]; then
  echo 'prerelease container metadata is incorrect' >&2
  exit 1
fi
