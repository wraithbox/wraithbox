#!/bin/sh
# Build and ad-hoc sign x25 with the virtualization entitlement into <scratch>/x25.
# Usage: build.sh <scratch dir>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$(cd "$1" && pwd)
(cd "$here" && swift build -c release --scratch-path "$scratch/x25-build")
cp "$scratch/x25-build/release/x25" "$scratch/x25"
codesign -f -s - --entitlements "$here/x25.entitlements" "$scratch/x25"
