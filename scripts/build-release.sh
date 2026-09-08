#!/usr/bin/env bash
# Cross-compile miodesk release binaries, checksums, and an update manifest.
# Usage: scripts/build-release.sh [version]   (COMMIT and REPOSITORY optional)
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-0.2.1-dev}"
VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "error: version must look like 1.2.3 or 1.2.3-rc1" >&2
  exit 2
fi

COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || true)}"
if [[ -z "$COMMIT" ]]; then COMMIT="unknown"; fi
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
REPOSITORY="${REPOSITORY:-Chillizu/miodesk}"

mkdir -p dist
# Only remove artifacts owned by this script. Other files in dist/ are left
# alone so a local checkout can keep unrelated build outputs there.
find dist -maxdepth 1 -type f \( -name 'miodesk-*' -o -name 'checksums.txt' -o -name 'manifest.json' \) -delete

platforms=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)
for platform in "${platforms[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"
  out="dist/miodesk-${os}-${arch}"
  if [[ "$os" == "windows" ]]; then out="${out}.exe"; fi
  echo "building ${out}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X github.com/Chillizu/miodesk/internal/buildinfo.Version=${VERSION} -X github.com/Chillizu/miodesk/internal/buildinfo.Commit=${COMMIT} -X github.com/Chillizu/miodesk/internal/buildinfo.BuildDate=${DATE}" \
    -o "$out" ./cmd/miodesk
done

(cd dist && sha256sum miodesk-* > checksums.txt)

manifest="dist/manifest.json"
{
  printf '{\n  "version": "%s",\n  "assets": {\n' "$VERSION"
  first=1
  for platform in "${platforms[@]}"; do
    os="${platform%/*}"
    arch="${platform#*/}"
    filename="miodesk-${os}-${arch}"
    if [[ "$os" == "windows" ]]; then filename="${filename}.exe"; fi
    sha="$(sha256sum "dist/${filename}" | awk '{print $1}')"
    if (( first == 0 )); then printf ',\n'; fi
    printf '    "%s": {"url": "https://github.com/%s/releases/download/v%s/%s", "sha256": "%s"}' \
      "$platform" "$REPOSITORY" "$VERSION" "$filename" "$sha"
    first=0
  done
  printf '\n  }\n}\n'
} > "$manifest"

echo "dist/ ready:"
ls -la dist/
