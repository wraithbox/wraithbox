#!/bin/sh
# X05-fs-benchmark spike: start x05-netd, wait for its socket, then boot the
# VM with x05, which hands it the NIC. Both log JSON lines to results/.
# Runs until the guest shuts down or <minutes> pass; then stops x05-netd.
# x05-netd allows no names: the guest reaches nothing but the SSH forward.
# Usage: run.sh <scratch dir> <name> <bundle> <minutes> [x05 flags...]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$(cd "$1" && pwd)
name=$2 bundle=$3 minutes=$4
shift 4
mac=$(sed -E 's/.*"mac":"([^"]*)".*/\1/' "$bundle/cfg.json")
[ -f "$scratch/stub.bin" ] || printf x >"$scratch/stub.bin"
rm -f "$scratch/netd.sock"
"$scratch/x05-netd" -sock "$scratch/netd.sock" -mac "$mac" -mtu 1500 \
  -stub "$scratch/stub.sock" -stub-file "$scratch/stub.bin" -stub-tcp 127.0.0.1:18085 \
  -allow none.invalid \
  >"$here/results/$name-netd.jsonl" 2>"$scratch/$name-netd.err" &
netd=$!
while [ ! -S "$scratch/netd.sock" ]; do sleep 0.1; done
"$scratch/x05" run "$bundle" "$scratch/netd.sock" "$minutes" "$@" \
  >"$here/results/$name-vmd.jsonl" 2>"$scratch/$name-vmd.err" || true
kill "$netd" 2>/dev/null || true
