#!/bin/sh
# Build only; this script never uploads or changes production.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo"
version=${VERSION:-v0.5.9-claude-stream-repair}
commit=${BUILD_COMMIT:-$(git rev-parse HEAD)}
build_date=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
out=${OUTPUT:-$repo/bin/CLIProxyAPI-claude-stream-repair}
mkdir -p "$(dirname -- "$out")"
# Explicit metadata replaces automatic VCS stamping. The deployed reference was
# built without automatic VCS metadata; preserve this for reproducibility.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath \
  -ldflags="-s -w -X github.com/router-for-me/CLIProxyAPI/v6/internal/buildinfo.Version=$version -X github.com/router-for-me/CLIProxyAPI/v6/internal/buildinfo.Commit=$commit -X github.com/router-for-me/CLIProxyAPI/v6/internal/buildinfo.BuildDate=$build_date" \
  -o "$out" ./cmd/server/
sha256sum "$out"
