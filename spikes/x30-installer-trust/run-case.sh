#!/bin/sh
# One install attempt: the confined x30 installs from <ipsw> into a new
# bundle <bundle>, while netwatch.sh records the host's flows and
# /usr/bin/log records the Virtualization and restore messages.
# Usage: run-case.sh <x30 binary> <case name> <ipsw> <bundle> <results dir>
set -u
here=$(cd "$(dirname "$0")" && pwd)
x30=$1 name=$2 ipsw=$3 bundle=$4 res=$5
mkdir -p "$res"
cache=$(getconf DARWIN_USER_CACHE_DIR | sed 's:/$::')
cache=$(cd "$cache" && pwd -P)
"$here/netwatch.sh" "$res/$name.net.csv" &
nw=$!
/usr/bin/log stream --style ndjson --level debug --predicate \
  'subsystem BEGINSWITH "com.apple.Virtualization" OR process BEGINSWITH "com.apple.Virtualization" OR subsystem CONTAINS "restore" OR eventMessage CONTAINS[c] "personaliz" OR eventMessage CONTAINS[c] "tss" OR eventMessage CONTAINS[c] "wkms" OR eventMessage CONTAINS[c] "img4"' \
  >"$res/$name.log.ndjson" 2>/dev/null &
lg=$!
date -u +%Y-%m-%dT%H:%M:%SZ >"$res/$name.start"
"$x30" --sb "$here/profiles/vmd-install.sb" \
  -D "BUNDLE=$bundle" -D "CACHE=$cache" -D "IPSW=$ipsw" \
  install "$bundle" "$ipsw" 8 >"$res/$name.jsonl" 2>"$res/$name.err"
rc=$?
echo "$rc" >"$res/$name.rc"
date -u +%Y-%m-%dT%H:%M:%SZ >"$res/$name.end"
sleep 2
kill "$nw" "$lg" 2>/dev/null
wait 2>/dev/null
echo "$name: exit $rc"
tail -3 "$res/$name.jsonl"
