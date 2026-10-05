#!/bin/sh
# X05-fs-benchmark spike: the kill-mid-write test, host side. Boots the run
# bundle, starts crash.mjs write on the data disk, and after <seconds>
# sends SIGKILL to one process of this VM: either "client" (this spike's
# x05 process) or "service" (the Virtualization XPC service that holds
# this VM's data image open, found with lsof on the image path, so no
# other VM is touched). Then boots again and checks what survived.
# Usage: crash.sh <scratch> <name> <bundle> <image> <cache> <sync> <client|service> <seconds>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$1 name=$2 bundle=$3 image=$4 cache=$5 sync=$6 target=$7 secs=$8
R="$here/results"
"$here/boot.sh" "$scratch" "$name-boot1" "$bundle" "$image" "$cache" "$sync"
"$here/gssh.sh" "$scratch" -scp "$here/crash.mjs" /Users/x05/crash.mjs
"$here/gssh.sh" "$scratch" -scp "$here/guest/fsck.sh" /Users/x05/fsck.sh
"$here/gssh.sh" "$scratch" rm -rf /Volumes/data/crash
"$here/gssh.sh" "$scratch" /Users/x05/tools/node/bin/node /Users/x05/crash.mjs write /Volumes/data/crash \
  >"$R/$name-acks.txt" 2>/dev/null &
sleep "$secs"
client=$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$R/$name-boot1-vmd.jsonl" | head -1)
if [ "$target" = client ]; then
  pid=$client
else
  pid=""
  for p in $(lsof -t "$image" 2>/dev/null); do
    [ "$p" = "$client" ] && continue
    case "$(ps -o comm= -p "$p")" in
      *Virtualization.VirtualMachine*) pid=$p ;;
    esac
  done
fi
[ -n "$pid" ] || { echo "no target process" >&2; exit 1; }
{
  echo "target=$target pid=$pid client=$client"
  ps -o pid,ppid,comm -p "$pid"
  echo "image holders before kill: $(lsof -t "$image" 2>/dev/null | tr '\n' ' ')"
  echo "last ack before kill: $(grep '^ack' "$R/$name-acks.txt" | tail -1)"
} >"$R/$name-kill.txt"
kill -9 "$pid"
echo "killed at $(date -u +%FT%T)" >>"$R/$name-kill.txt"
while pgrep -x x05-netd >/dev/null; do sleep 1; done
i=0
while [ -n "$(lsof -t "$image" 2>/dev/null)" ] && [ $i -lt 60 ]; do sleep 1; i=$((i + 1)); done
sleep 2
{
  echo "image holders after: $(lsof -t "$image" 2>/dev/null | tr '\n' ' ')"
  echo "last ack seen by host: $(grep '^ack' "$R/$name-acks.txt" | tail -1)"
  echo "last file seen by host: $(grep '^file' "$R/$name-acks.txt" | tail -1)"
} >>"$R/$name-kill.txt"
"$here/boot.sh" "$scratch" "$name-boot2" "$bundle" "$image" "$cache" "$sync"
"$here/gssh.sh" "$scratch" sh /Users/x05/fsck.sh >"$R/$name-check.txt" 2>&1 || true
"$here/gstop.sh" "$scratch"
cat "$R/$name-kill.txt" "$R/$name-check.txt"
