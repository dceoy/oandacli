#!/usr/bin/env bash
set -euo pipefail

# Keep the version safe for both ldflags and asset names.
if [[ ! "${RELEASE_TAG:-}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'RELEASE_TAG must be a stable version such as v0.1.0' >&2
  exit 1
fi
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 \
    go build -trimpath \
      -ldflags "-s -w -X github.com/dceoy/oandactl/internal/cli.Version=${RELEASE_TAG}" \
      -o "dist/oandactl_${RELEASE_TAG}_${target%/*}_${target#*/}" .
done
(
  cd dist
  sha256sum "oandactl_${RELEASE_TAG}_"* > SHA256SUMS
)
