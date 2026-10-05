#!/bin/sh
# X05-fs-benchmark spike: for each caching and synchronization mode, boot
# the run bundle with the data image, run bench.mjs in the guest, shut the
# guest down, then run bench.mjs on the host while no VM runs.
# Usage: matrix.sh <scratch dir> <bundle> <image> <prefix> <runs> <cache:sync>...
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$1 bundle=$2 image=$3 prefix=$4 runs=$5
shift 5
W=$HOME/Library/Caches/wraithbox-spikes/x05/work.noindex
for cs in "$@"; do
  cache=${cs%%:*} sync=${cs##*:}
  name="$prefix-$cache-$sync"
  echo "== $name $(date)"
  "$here/boot.sh" "$scratch" "$name" "$bundle" "$image" "$cache" "$sync"
  "$here/gbench.sh" "$scratch" "$name" "$runs" >/dev/null
  "$here/gstop.sh" "$scratch"
  sleep 5
  echo "== host after $name $(date)"
  "$W/tools/node/bin/node" "$here/bench.mjs" "$W/tools" "$W" "host-after-$name" "$runs" \
    >>"$here/results/host-bench.jsonl"
done
echo "== matrix done $(date)"
