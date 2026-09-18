#!/usr/bin/env bash
# Read-only release preflight for a normal clone of github.com/Chillizu/miodesk.
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ ! -s LICENSE ]]; then
  echo "[FAIL] LICENSE must be present for a public release" >&2
  exit 1
fi

if [[ -n "$(git ls-files -- 'miodesk' 'miodesk-*' 'dist/*')" ]]; then
  echo "[FAIL] tracked build outputs must be removed before a public release:" >&2
  git ls-files -- 'miodesk' 'miodesk-*' 'dist/*' >&2
  exit 1
fi

formatted="$(git ls-files '*.go' | xargs gofmt -l)"
if [[ -n "$formatted" ]]; then
  echo "[FAIL] gofmt required:" >&2
  printf '%s\n' "$formatted" >&2
  exit 1
fi

echo "[INFO] go vet ./..."
go vet ./...
echo "[INFO] go mod verify"
go mod verify
echo "[INFO] go test -race ./..."
go test -race ./...
echo "[INFO] go build ./cmd/miodesk"
build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT
go build -buildvcs=false -o "$build_dir/miodesk" ./cmd/miodesk
echo "[INFO] installer smoke test"
scripts/install-test.sh
echo "[OK] release preflight passed"
