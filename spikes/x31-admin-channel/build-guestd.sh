#!/bin/sh
# Build wb-guestd from this checkout twice, stamped 0.0.0-x31.1 (installed)
# and 0.0.0-x31.2 (on the tools disk). Usage: build-guestd.sh <out dir>
set -eu
out=$(cd "$1" && pwd)
cd "$(dirname "$0")/../../packages/wraithbox-go"
pkg=github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version.stamp
for v in 1 2; do
  GOOS=darwin GOARCH=arm64 go build -ldflags "-X $pkg=0.0.0-x31.$v" -o "$out/wb-guestd.$v" ./cmd/wb-guestd
done
