#!/bin/bash
# Usage: probe.sh <binary> <mech> <profile> [extra args]
# Runs a probe binary with a mechanism and profile from the spike directory.
cd "$(dirname "$0")" || exit 1
bin=$1 mech=$2 prof=$3
shift 3
if [ "$mech" = exec ]; then
  sandbox-exec -f "$prof" -D "BIN=$PWD/bin" "./bin/$bin" -mech none -readfile "$PWD/go.mod" -writedir "$PWD" "$@" 2>&1
else
  "./bin/$bin" -mech "$mech" -profile "$prof" -D "BIN=$PWD/bin" -readfile "$PWD/go.mod" -writedir "$PWD" "$@" 2>&1
fi
echo "exit=$?"
