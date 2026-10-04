#!/bin/sh
# X02-warm-start: N cold boots, each from a fresh APFS clone of <bundle>,
# so disk blocks are not in the host page cache for the new files.
# Usage: cold-series.sh <x02 binary> <bundle> <runs> <out.jsonl>
set -u
x02=$1
src=$2
runs=$3
out=$4
cd "$HOME/Library/Caches/wraithbox-spikes/x02" || exit 1
i=1
while [ "$i" -le "$runs" ]; do
  rm -rf coldseries
  cp -c -R "$src" coldseries
  rm -f coldseries/state.vzvmsave
  "$x02" cold coldseries 1 >>"$out" 2>/dev/null
  i=$((i + 1))
  sleep 2
done
rm -rf coldseries
