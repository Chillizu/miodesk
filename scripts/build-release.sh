#!/usr/bin/env bash
# Cross-compile miodesk release binaries into dist/ with sha256 checksums.
# Usage: scripts/build-release.sh [version]   (COMMIT env optional)
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-0.0.0-dev}"
COMMIT="${COMMIT:-unknown}"
DATE="$(date +%Y-%m-%d)"

rm -rf dist
mkdir -p dist

for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os="${platform%/*}"
  arch="${platform#*/}"
  out="dist/miodesk-${os}-${arch}"
  if [ "$os" = "windows" ]; then out="${out}.exe"; fi
  echo "building ${out}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X miodesk/internal/buildinfo.Version=${VERSION} -X miodesk/internal/buildinfo.Commit=${COMMIT} -X miodesk/internal/buildinfo.BuildDate=${DATE}" \
    -o "$out" ./cmd/miodesk
done

(cd dist && sha256sum miodesk-* > checksums.txt)
echo "dist/ ready:"
ls -la dist/
