#!/bin/sh
# X05-fs-benchmark spike: the "git status on a large repository" workload,
# a shallow clone of nodejs/node at a fixed tag, and the Go incremental
# build workload, x05-netd with its modules vendored (gVisor's TCP/IP
# stack, about 1000 Go files).
# Usage: prep-repo.sh <work dir> <spike dir>
set -eu
W=$1 S=$2
G="$W/tools/vcs/bin/git"
export GIT_EXEC_PATH="$W/tools/vcs/git-core"
rm -rf "$W/repo"
"$G" clone -q --depth 1 --branch v24.9.0 https://github.com/nodejs/node "$W/repo"
"$G" -C "$W/repo" status --short | wc -l
du -sh "$W/repo" "$W/repo/.git"
"$G" -C "$W/repo" ls-files | wc -l

rm -rf "$W/gobuild"
mkdir -p "$W/gobuild"
cp -R "$S/go/." "$W/gobuild/"
cd "$W/gobuild"
export PATH="$W/tools/go/bin:$PATH" GOTOOLCHAIN=local GOWORK=off GOFLAGS=-mod=vendor
GOFLAGS= go mod vendor
find vendor -name '*.go' | wc -l
du -sh "$W/gobuild"
