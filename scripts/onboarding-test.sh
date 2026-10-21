#!/usr/bin/env bash
# Fresh-user, no-credentials setup smoke in a disposable workspace and XDG
# config. Exercises the real binary and a real MCP client without touching the
# installed miodesk Core or the optional Tunnel.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
tmp=$(mktemp -d)
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT
mkdir -p "$tmp/workspace" "$tmp/config" "$tmp/data" "$tmp/state"
go build -buildvcs=false -o "$tmp/miodesk" ./cmd/miodesk
go build -buildvcs=false -o "$tmp/mcp-smoke" ./scripts/mcp-smoke
export XDG_CONFIG_HOME="$tmp/config" XDG_DATA_HOME="$tmp/data" XDG_STATE_HOME="$tmp/state"

# Redirecting from /dev/null must not trigger the interactive wizard. The
# workspace root and its configuration are freshly created.
(cd "$tmp/workspace" && "$tmp/miodesk" setup --workspace "$PWD" </dev/null) >"$tmp/setup.log" 2>&1
grep -q '\[OK\] configuration ready:' "$tmp/setup.log"
grep -q '\[INFO\] local MCP only' "$tmp/setup.log"
if grep -Eq 'Workspace root \[|Configure OpenAI Secure MCP Tunnel now \[' "$tmp/setup.log"; then
  echo "onboarding unexpectedly entered the interactive wizard" >&2
  exit 1
fi
"$tmp/miodesk" doctor >"$tmp/doctor.log"
grep -q '\[OK\] local use: ready' "$tmp/doctor.log"
test -f "$tmp/config/miodesk/config.toml"
test ! -e "$tmp/config/tunnel-client"
echo '[OK] non-interactive fresh setup and local readiness'

# Use an ephemeral port to avoid colliding with any running host service.
"$tmp/miodesk" serve --port 0 >"$tmp/serve.log" 2>&1 &
pid=$!
endpoint=""
for i in $(seq 1 100); do
  endpoint=$(sed -n 's/^\[OK\] MCP server: \(http[^ ]*\/mcp\).*$/\1/p' "$tmp/serve.log" | tail -1)
  if [[ -n "$endpoint" ]]; then break; fi
  if ! kill -0 "$pid" 2>/dev/null; then
    cat "$tmp/serve.log" >&2
    echo "fresh Core exited unexpectedly" >&2
    exit 1
  fi
  sleep 0.1
done
if [[ -z "$endpoint" ]]; then
  cat "$tmp/serve.log" >&2
  echo "fresh Core did not announce its endpoint" >&2
  exit 1
fi
"$tmp/mcp-smoke" -endpoint "$endpoint"
echo '[OK] fresh-user MCP roundtrip and session lifecycle'
