#!/usr/bin/env bash
# Cross-compile both programs for every supported platform and package each one
# into a single archive (plus checksums) under dist/.
#
#   VERSION=v1.2.3 ./scripts/build-release.sh   # VERSION defaults to "dev"
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-dev}"
BINARIES=(modpack-send modpack-receive)
TARGETS=(
  windows/amd64
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
)

rm -rf dist
mkdir -p dist

for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  ext=""
  [ "$GOOS" = windows ] && ext=".exe"

  name="modpack-sync-${VERSION}-${GOOS}-${GOARCH}"
  stage="dist/${name}"
  mkdir -p "$stage"

  for bin in "${BINARIES[@]}"; do
    echo "building ${bin} for ${GOOS}/${GOARCH}"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -ldflags="-s -w" \
      -o "${stage}/${bin}${ext}" "./cmd/${bin}"
  done

  cp ./*.md "$stage"/

  if [ "$GOOS" = windows ]; then
    (cd dist && zip -qr "${name}.zip" "$name")
  else
    tar -czf "dist/${name}.tar.gz" -C dist "$name"
  fi
  rm -rf "$stage"
done

(cd dist && sha256sum ./*.zip ./*.tar.gz >checksums.txt)

echo
echo "artifacts in dist/:"
ls -1 dist
