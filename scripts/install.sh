#!/bin/sh
# Install a verified miodesk release binary into a user-writable bin directory.
set -eu

REPOSITORY="${MIODESK_REPOSITORY:-Chillizu/miodesk}"
VERSION="${MIODESK_VERSION:-latest}"
BIN_DIR="${MIODESK_BIN_DIR:-$HOME/.local/bin}"
RELEASE_BASE="${MIODESK_RELEASE_BASE:-}"

usage() {
    cat <<'EOF'
Usage: install.sh [options]

Options:
  --version VERSION   install a specific release (for example 0.2.1 or v0.2.1)
  --bin-dir DIR       install directory (default: ~/.local/bin)
  --repo OWNER/REPO   GitHub repository (default: Chillizu/miodesk)
  -h, --help          show this help

Environment equivalents:
  MIODESK_VERSION
  MIODESK_BIN_DIR
  MIODESK_REPOSITORY
  MIODESK_RELEASE_BASE   override release base URL (mainly for mirrors/tests)
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] || { echo "Error: --version requires a value" >&2; exit 2; }
            VERSION="$2"
            shift 2
            ;;
        --version=*)
            VERSION="${1#--version=}"
            shift
            ;;
        --bin-dir)
            [ "$#" -ge 2 ] || { echo "Error: --bin-dir requires a value" >&2; exit 2; }
            BIN_DIR="$2"
            shift 2
            ;;
        --bin-dir=*)
            BIN_DIR="${1#--bin-dir=}"
            shift
            ;;
        --repo)
            [ "$#" -ge 2 ] || { echo "Error: --repo requires a value" >&2; exit 2; }
            REPOSITORY="$2"
            shift 2
            ;;
        --repo=*)
            REPOSITORY="${1#--repo=}"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: unknown option: $1" >&2
            echo "Hint: run install.sh --help" >&2
            exit 2
            ;;
    esac
done

case "$VERSION" in
    latest) ;;
    v*) VERSION="${VERSION#v}" ;;
esac

os_name=$(uname -s 2>/dev/null || true)
case "$os_name" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *)
        echo "Error: unsupported operating system: ${os_name:-unknown}" >&2
        echo "Hint: download the matching release asset manually." >&2
        exit 1
        ;;
esac

machine=$(uname -m 2>/dev/null || true)
case "$machine" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *)
        echo "Error: unsupported architecture: ${machine:-unknown}" >&2
        exit 1
        ;;
esac

asset="miodesk-${os}-${arch}"
if [ -n "$RELEASE_BASE" ]; then
    base="${RELEASE_BASE%/}"
elif [ "$VERSION" = "latest" ]; then
    base="https://github.com/$REPOSITORY/releases/latest/download"
else
    base="https://github.com/$REPOSITORY/releases/download/v$VERSION"
fi

download() {
    url="$1"
    out="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --connect-timeout 10 --output "$out" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$out" "$url"
    else
        echo "Error: curl or wget is required to download miodesk." >&2
        exit 1
    fi
}

sha256() {
    file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        echo "Error: sha256sum or shasum is required to verify miodesk." >&2
        exit 1
    fi
}

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t miodesk-install)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

echo "[INFO] downloading $asset"
download "$base/$asset" "$tmp/$asset"
download "$base/checksums.txt" "$tmp/checksums.txt"

expected=$(awk -v name="$asset" '
    $2 == name || $2 == "*" name { print $1; exit }
' "$tmp/checksums.txt")
if [ -z "$expected" ]; then
    echo "Error: $asset is missing from checksums.txt" >&2
    exit 1
fi
actual=$(sha256 "$tmp/$asset")
if [ "$actual" != "$expected" ]; then
    echo "Error: checksum mismatch for $asset" >&2
    echo "Expected: $expected" >&2
    echo "Actual:   $actual" >&2
    exit 1
fi
echo "[OK] checksum verified"

mkdir -p "$BIN_DIR"
target="$BIN_DIR/miodesk"
stage=$(mktemp "$BIN_DIR/.miodesk.XXXXXX")
trap 'rm -rf "$tmp"; rm -f "$stage"' EXIT HUP INT TERM
cat "$tmp/$asset" > "$stage"
chmod 755 "$stage"
mv -f "$stage" "$target"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

echo "[OK] installed: $target"
if "$target" version >/dev/null 2>&1; then
    "$target" version | sed -n '1p'
fi

case ":${PATH:-}:" in
    *":$BIN_DIR:"*) ;;
    *)
        echo "[WARN] $BIN_DIR is not on PATH"
        echo "       Add it to your shell profile, then open a new terminal."
        ;;
esac

cat <<EOF

Next:
  cd /path/to/workspace
  $target setup
  $target doctor

On Linux, after setup is healthy, persistent services are optional:
  $target service install
  $target service start
EOF
