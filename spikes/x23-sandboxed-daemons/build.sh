#!/bin/bash
# Builds every spike binary into bin/: cgo builds plain, no-cgo builds
# with a -nocgo suffix. The spike module is not in the repository go.work.
set -e
cd "$(dirname "$0")"
export GOWORK=off
go vet ./...
go build -o bin/ ./cmd/...
for b in netd probe hostd; do
  CGO_ENABLED=0 go build -o "bin/$b-nocgo" "./cmd/$b"
done
