#!/bin/sh
# Builds two ad-hoc signed, hardened-runtime copies with different cdhashes.
# ./build.sh [suffix]  -- a suffix changes the binaries, as an upgrade would.
set -eu
cd "$(dirname "$0")"
mkdir -p bin
for v in a b; do
	GOWORK=off go build -o "bin/x9-$v" -ldflags "-X main.variant=$v${1:-}" .
	codesign -f -s - -o runtime --identifier "nl.wraithbox.spike.x9-$v" "bin/x9-$v"
	codesign -dvvv "bin/x9-$v" 2>&1 | grep -E '^(Identifier|CDHash|Signature|flags)' | sed "s/^/x9-$v: /"
done
