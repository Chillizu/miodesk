#!/usr/bin/env bash
# Reproducible, isolated opt-in Code Mode MCP smoke, without touching the host
# miodesk Core, Tunnel service, credentials or installed configuration.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
scratch=$(mktemp -d)
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT
mkdir -p "$scratch/config/miodesk" "$scratch/workspace/project"
printf 'hello codemode\n' > "$scratch/workspace/project/hello.txt"
port=18991
printf '[codemode]\nenabled = true\n[server]\nhost = "127.0.0.1"\nport = %s\n[workspace]\nroot = "%s"\n' "$port" "$scratch/workspace" > "$scratch/config/miodesk/config.toml"
go build -buildvcs=false -o "$scratch/miodesk" ./cmd/miodesk
XDG_CONFIG_HOME="$scratch/config" "$scratch/miodesk" serve > "$scratch/serve.log" 2>&1 &
pid=$!
for i in $(seq 1 80); do
  if curl -fsS --max-time 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$pid" 2>/dev/null; then cat "$scratch/serve.log" >&2; exit 1; fi
  sleep 0.1
done
curl -fsS --max-time 5 "http://127.0.0.1:$port/healthz" > /dev/null
go run ./scripts/codemode-smoke -endpoint "http://127.0.0.1:$port/mcp"
if grep -q 'MIODESK_PRIVATE_LOG_SENTINEL' "$scratch/serve.log"; then
  echo 'JavaScript exception contents leaked to Core logs' >&2
  exit 1
fi
printf '[OK] script exception body absent from Core logs\n'
test ! -e "$scratch/workspace/project/hacked.txt"
echo '[OK] Code Mode MCP smoke complete'
