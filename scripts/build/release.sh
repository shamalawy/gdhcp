#!/bin/bash
# Cross-builds gdhcp for linux/amd64 and linux/arm64 (the two supported
# server platforms), and packages each as a tarball with a sha256 checksum
# under dist/release/. The web build runs once and is embedded into both.
#
# Usage:
#   VERSION=v1.0.0 ./scripts/build/release.sh
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

PLATFORMS=("linux/amd64" "linux/arm64")
OUT_DIR="dist/release"

echo "==> building web assets (web/dist) — shared across all platforms"
( cd web && npm ci && npm run build )

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

for platform in "${PLATFORMS[@]}"; do
    GOOS="${platform%/*}"
    GOARCH="${platform#*/}"
    BIN_DIR="$(mktemp -d)"
    trap 'rm -rf "$BIN_DIR"' EXIT

    echo "==> building ${GOOS}/${GOARCH}"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
        -ldflags "-X github.com/shamalawy/gdhcp/server/internal/version.Version=${VERSION} \
                  -X github.com/shamalawy/gdhcp/server/internal/version.Commit=${COMMIT} \
                  -X github.com/shamalawy/gdhcp/server/internal/version.BuildDate=${BUILD_DATE} \
                  -w -s" \
        -o "${BIN_DIR}/gdhcp" ./server/cmd/gdhcp

    TAR_NAME="gdhcp-${VERSION}-${GOOS}-${GOARCH}.tar.gz"
    tar -czf "${OUT_DIR}/${TAR_NAME}" \
        -C "$BIN_DIR" gdhcp \
        -C "$REPO_ROOT" config.yaml.example

    rm -rf "$BIN_DIR"
    trap - EXIT
    echo "==> packaged ${OUT_DIR}/${TAR_NAME}"
done

echo "==> writing checksums"
( cd "$OUT_DIR" && sha256sum ./*.tar.gz > "SHA256SUMS.txt" )

echo "==> release artifacts ready in ${OUT_DIR}/"
ls -lh "$OUT_DIR"
