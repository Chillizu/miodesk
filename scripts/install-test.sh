#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "skip: unsupported test OS"; exit 0 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "skip: unsupported test arch"; exit 0 ;;
esac

asset="miodesk-${os}-${arch}"
release="$TMP/release"
bin="$TMP/bin"
mkdir -p "$release" "$bin"

cat > "$release/$asset" <<'EOF'
#!/bin/sh
if [ "${1:-}" = version ]; then
  echo "miodesk install-test"
fi
EOF
chmod +x "$release/$asset"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$release" && sha256sum "$asset" > checksums.txt)
else
  sha=$(shasum -a 256 "$release/$asset" | awk '{print $1}')
  printf '%s  %s\n' "$sha" "$asset" > "$release/checksums.txt"
fi

python3 -u -m http.server 0 --bind 127.0.0.1 --directory "$release" >"$TMP/http.log" 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; rm -rf "$TMP"' EXIT

for _ in {1..50}; do
  port=$(python3 - "$TMP/http.log" <<'PY'
import re, sys
try:
    text = open(sys.argv[1]).read()
except OSError:
    text = ""
m = re.search(r"port (\d+)", text)
print(m.group(1) if m else "")
PY
)
  [ -n "$port" ] && break
  sleep 0.05
done
[ -n "${port:-}" ] || { cat "$TMP/http.log"; echo "installer test server failed" >&2; exit 1; }

MIODESK_RELEASE_BASE="http://127.0.0.1:$port" MIODESK_BIN_DIR="$bin" PATH="$PATH" sh "$ROOT/scripts/install.sh" >"$TMP/install.out"

grep -q "\[OK\] checksum verified" "$TMP/install.out"
grep -q "\[OK\] installed:" "$TMP/install.out"
test -x "$bin/miodesk"
test "$("$bin/miodesk" version)" = "miodesk install-test"

# A corrupted release must fail verification before touching the installed file.
printf '\n# corrupted\n' >> "$release/$asset"
if MIODESK_RELEASE_BASE="http://127.0.0.1:$port" MIODESK_BIN_DIR="$bin" PATH="$PATH" sh "$ROOT/scripts/install.sh" >"$TMP/bad.out" 2>&1; then
  echo "installer unexpectedly accepted a bad checksum" >&2
  exit 1
fi
grep -q "checksum mismatch" "$TMP/bad.out"
test "$("$bin/miodesk" version)" = "miodesk install-test"

printf 'install-test: ok\n'
