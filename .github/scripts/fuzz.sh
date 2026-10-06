#!/usr/bin/env bash
# Runs every Fuzz* target in the module for the given duration each.
# Usage: fuzz.sh <duration>
set -euo pipefail
fuzztime="${1:-30s}"
for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    echo "::group::$pkg $target"
    go test -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" "$pkg"
    echo "::endgroup::"
  done
done
