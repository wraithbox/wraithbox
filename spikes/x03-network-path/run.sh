#!/bin/sh
# X03-network-path spike: start x03-netd, wait for its socket, then boot the
# VM with x03, which hands it the NIC. Both log JSON lines to results/.
# Runs until the guest shuts down or <minutes> pass; then stops x03-netd.
# Usage: run.sh <scratch dir> <name> <bundle> <minutes> <mtu> [x03-netd flags...]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$(cd "$1" && pwd)
name=$2 bundle=$3 minutes=$4 mtu=$5
shift 5
mac=$(sed -E 's/.*"mac":"([^"]*)".*/\1/' "$bundle/cfg.json")
rm -f "$scratch/netd.sock"
"$scratch/x03-netd" -sock "$scratch/netd.sock" -mac "$mac" -mtu "$mtu" \
  -stub "$scratch/stub.sock" -stub-file "$scratch/bulk-1g.bin" "$@" \
  >"$here/results/$name-netd.jsonl" 2>"$scratch/$name-netd.err" &
netd=$!
while [ ! -S "$scratch/netd.sock" ]; do sleep 0.1; done
"$scratch/x03" run "$bundle" "$scratch/netd.sock" "$minutes" --mtu "$mtu" \
  >"$here/results/$name-vmd.jsonl" 2>"$scratch/$name-vmd.err" || true
kill "$netd"
