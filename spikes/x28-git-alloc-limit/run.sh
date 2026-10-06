#!/bin/sh
# Runs the X26 harness with Homebrew git and Apple git, one after the
# other, and writes the output to results/. Takes the bare clone of a
# large repository (golang/go) as its only argument.
set -eu
cd "$(dirname "$0")"
big=${1:-$(cd ../.. && pwd)/.scratch/cache/go.git}
work=${WORK:-$(cd ../.. && pwd)/.scratch}
export GOWORK=off
go test ./...
mkdir -p results
go run ./cmd/x26 -git /opt/homebrew/bin/git -big "$big" -runs 3 -work "$work" > results/run1-homebrew.txt 2>&1
go run ./cmd/x26 -git /usr/bin/git -big "$big" -runs 3 -work "$work" > results/run2-apple.txt 2>&1
