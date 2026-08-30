#!/bin/bash
# Single-target build: Angular frontend + the gdhcp binary for the current
# GOOS/GOARCH (or the ones given), CGO_ENABLED=0 per amendment A3. Simpler
# alternative to `make build` for CI or ad hoc use; see release.sh for
# multi-arch release tarballs.
#
# Usage:
#   ./scripts/build/build.sh                  # build for host GOOS/GOARCH
#   GOOS=linux GOARCH=arm64 ./scripts/build/build.sh
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

echo "==> building web assets (web/dist)"
( cd web && npm ci && npm run build )

echo "==> building gdhcp for ${GOOS}/${GOARCH} (version ${VERSION}, commit ${COMMIT})"
mkdir -p dist
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
    -ldflags "-X github.com/shamalawy/gdhcp/server/internal/version.Version=${VERSION} \
              -X github.com/shamalawy/gdhcp/server/internal/version.Commit=${COMMIT} \
              -X github.com/shamalawy/gdhcp/server/internal/version.BuildDate=${BUILD_DATE} \
              -w -s" \
    -o "dist/gdhcp" ./server/cmd/gdhcp

echo "==> built dist/gdhcp"
