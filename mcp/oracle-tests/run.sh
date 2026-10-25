#!/bin/sh
# Runs one scripted Oracle: run.sh "<go packages>" "<test name pattern>".
# Without arguments, runs every scripted Oracle (the whole Go test suite, race detector on).
# Exit status 0 = pass. A "blocked:" skip (e.g. Node missing) is reported, not counted as a pass.
set -eu
cd "$(dirname "$0")/.."
if [ $# -eq 0 ]; then
  exec go test -race -count=1 ./...
fi
# shellcheck disable=SC2086
out=$(go test -race -count=1 -v -run "$2" $1 2>&1) || { printf '%s\n' "$out"; exit 1; }
printf '%s\n' "$out" | grep -E '^(=== RUN|--- |ok|PASS|FAIL)' || true
if printf '%s\n' "$out" | grep -q 'blocked:'; then
  echo "BLOCKED: a prerequisite is missing (see above); this is not a pass." >&2
  exit 2
fi
