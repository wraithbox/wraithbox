#!/bin/sh
# X18-vsock-handoff: N restores (or cold boots), each from a fresh APFS clone
# of <bundle>, so no file is in the host page cache.
# Usage: series.sh restore|cold <x18> <bundle dir> <recv sock> <runs> <out.jsonl>
set -u
mode=$1 x18=$2 src=$3 sock=$4 runs=$5 out=$6
dir=$(dirname "$src")
i=1
while [ "$i" -le "$runs" ]; do
  # Restore and save need the host unlocked (X02-warm-start): wait for it,
  # and record the state each run starts in.
  while ioreg -n Root -d1 | grep -q '"IOConsoleLocked" = Yes'; do sleep 10; done
  echo "run $i $(date -u +%FT%TZ) $(ioreg -n Root -d1 | grep -o '"IOConsoleLocked"[^,}]*')" >>"$out.lock"
  rm -rf "$dir/series"
  cp -c -R "$src" "$dir/series"
  if [ "$mode" = restore ]; then
    "$x18" restore "$dir/series" "$sock" >>"$out" 2>>"$out.err"
  else
    "$x18" prepare "$dir/series" "$sock" 0 >>"$out" 2>>"$out.err"
  fi
  i=$((i + 1))
  sleep 2
done
rm -rf "$dir/series"
