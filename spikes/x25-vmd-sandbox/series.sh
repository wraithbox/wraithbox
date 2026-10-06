#!/bin/sh
# Restore a saved bundle N times confined and N times unconfined,
# alternating, each from a fresh APFS clone, and print the restore times.
# Usage: series.sh <saved bundle> <work dir> <cache dir> <profile> <n>
set -u
here=$(cd "$(dirname "$0")" && pwd)
src=$1 work=$2 cache=$3 prof=$4 n=$5
i=1
while [ "$i" -le "$n" ]; do
  for mode in confined unconfined; do
    b="$work/series-$mode-$i"
    rm -rf "$b"
    cp -c -R "$src" "$b"
    if [ "$mode" = confined ]; then
      "$here/run.sh" "series-$mode-$i" --sb "$prof" -D BUNDLE="$b" -D CACHE="$cache" restore "$b" >/dev/null 2>&1
    else
      "$here/run.sh" "series-$mode-$i" restore "$b" >/dev/null 2>&1
    fi
    printf '%s\t%s\t' "$mode" "$i"
    grep -h '"step":"restore"' "$here/results/series-$mode-$i.jsonl" | sed -E 's/.*"restoreSeconds":([0-9.]+).*"vsockSeconds":([0-9.]+).*/restore \1 vsock \2/'
    rm -rf "$b"
  done
  i=$((i + 1))
done
