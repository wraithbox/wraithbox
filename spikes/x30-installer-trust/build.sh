#!/bin/sh
# Build and ad-hoc sign x30 with the virtualization entitlement into <scratch>/x30.
# Usage: build.sh <scratch dir>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$(cd "$1" && pwd)
(cd "$here" && swift build -c release --scratch-path "$scratch/x30-build")
rm -f "$scratch/x30"
cp "$scratch/x30-build/release/x30" "$scratch/x30"
codesign -f -s - --entitlements "$here/x30.entitlements" "$scratch/x30"
