#!/bin/sh
# X02-warm-start: reuse of one saved state through APFS clones.
# Usage: clone-tests.sh <x02 binary> <results dir>
# Needs ~/Library/Caches/wraithbox-spikes/x02/base with state.vzvmsave
# (left by `x02 cycle`). Bundles are APFS clones (cp -c), so they share
# blocks with base until a guest writes.
set -u
x02=$1
out=$2
cd "$HOME/Library/Caches/wraithbox-spikes/x02" || exit 1
clone() { rm -rf "$1"; cp -c -R base "$1"; }
run() { echo "== $*" >&2; "$x02" "$@" 2>>"$out/clone.err"; }

if [ "${PART2:-}" != 1 ]; then
# 1. One saved state, two clones, each restored (sequentially).
clone c1
clone c2
run restore c1 10 >>"$out/clone.jsonl"
run restore c2 10 >>"$out/clone.jsonl"
# 2. Restore c1 again: its disks have moved on since the state was saved.
run restore c1 10 >>"$out/clone.jsonl"
# 3. Restore base itself, untouched by 1 and 2.
clone c3
run restore c3 10 >>"$out/clone.jsonl"
# 4. Same state with a fresh machine identifier, and with a fresh MAC.
clone c4
run restore c4 5 --new-mid >>"$out/clone.jsonl"
clone c5
run restore c5 5 --new-mac >>"$out/clone.jsonl"
# 5. Two clones of one state restored at the same time.
clone c6
clone c7
run restore c6 30 >>"$out/concurrent-c6.jsonl" &
run restore c7 30 >>"$out/concurrent-c7.jsonl" &
wait
fi

# 6. Repeat of 5, and two cold boots of clones (same machine identifier)
#    at the same time, to separate "same saved state" from "same identity".
if [ "${PART2:-}" = 1 ]; then
  clone c8
  clone c9
  run restore c8 30 >>"$out/concurrent2-c8.jsonl" &
  run restore c9 30 >>"$out/concurrent2-c9.jsonl" &
  wait
  clone c10
  clone c11
  run cold c10 1 >>"$out/concurrent-cold-c10.jsonl" &
  sleep 1
  run cold c11 1 >>"$out/concurrent-cold-c11.jsonl" &
  wait
fi

# 7. Separate "two restores at once" from "same identity":
#    base2 = clone of base with a new machine identifier and its own state.
if [ "${PART3:-}" = 1 ]; then
  if [ ! -f base2/state.vzvmsave ]; then
    clone base2
    rm -f base2/state.vzvmsave
    "$x02" newmid base2
    run cycle base2 1 30 >>"$out/base2-cycle.jsonl"
  fi
  # B: restore clones of two different states at the same time
  clone c12
  rm -rf c13; cp -c -R base2 c13
  run restore c12 30 >>"$out/concurrent3-c12.jsonl" &
  run restore c13 30 >>"$out/concurrent3-c13.jsonl" &
  wait
  # C: restore a clone of base while a cold-booted clone of base runs
  clone c14
  clone c15
  run explore c14 60 2>>"$out/concurrent4-c14.log" &
  sleep 15
  run restore c15 10 >>"$out/concurrent4-c15.jsonl"
  wait
fi
du -sh base c* >&2
