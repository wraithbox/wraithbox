#!/bin/sh
# The thin-pack phase, added after run1 and run2, with both gits.
set -eu
cd "$(dirname "$0")"
big=${1:-$(cd ../.. && pwd)/.scratch/cache/go.git}
work=${WORK:-$(cd ../.. && pwd)/.scratch}
export GOWORK=off
go run ./cmd/x26 -git /opt/homebrew/bin/git -big "$big" -phases thin -work "$work" > results/run3-thin-homebrew.txt 2>&1
go run ./cmd/x26 -git /usr/bin/git -big "$big" -phases thin -work "$work" > results/run4-thin-apple.txt 2>&1
