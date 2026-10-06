#!/bin/sh
# X28: reruns the X26 bomb packs with GIT_ALLOC_LIMIT, with Homebrew git
# and Apple git, and writes the output to results/. Takes the bare clone
# of a large repository (golang/go) as its only argument.
set -eu
cd "$(dirname "$0")"
big=${1:-$(cd ../.. && pwd)/.scratch/cache/go.git}
work=${WORK:-$(cd ../.. && pwd)/.scratch}
export GOWORK=off
go test ./...
mkdir -p results
go run ./cmd/x26 -phases alloc,alloccost -git /opt/homebrew/bin/git -big "$big" -runs 3 -work "$work" > results/x28-run1-homebrew.txt 2>&1
go run ./cmd/x26 -phases alloc,alloccost -git /usr/bin/git -big "$big" -runs 3 -work "$work" > results/x28-run2-apple.txt 2>&1
