#!/bin/sh
# Cross-compile release archives into ./dist. Usage: scripts/build-all.sh [version]
set -eu
VERSION="${1:-dev}"
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  name="hloop_${VERSION}_${os}_${arch}"
  bin="hloop"; [ "$os" = "windows" ] && bin="hloop.exe"
  stage="dist/$name"; mkdir -p "$stage"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$stage/$bin" ./cmd/hloop
  cp README.md SECURITY.md LICENSE "$stage/"
  if [ "$os" = "windows" ]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done
(cd dist && sha256sum hloop_* > checksums.txt)
ls -la dist
