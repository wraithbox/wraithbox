#!/bin/sh
# X05-fs-benchmark spike: boot the run bundle with a data disk in the
# background, and wait until the guest answers on SSH.
# Usage: boot.sh <scratch dir> <name> <bundle> <data image> <cache> <sync> [more x05 flags]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$1 name=$2 bundle=$3 data=$4 cache=$5 sync=$6
shift 6
if pgrep -x x05-netd >/dev/null; then echo "a VM is still running" >&2; exit 1; fi
"$here/run.sh" "$scratch" "$name" "$bundle" 60 --data "$data" --cache "$cache" --sync "$sync" --mem 8 "$@" &
t0=$(date +%s)
sleep 15
until "$here/gssh.sh" "$scratch" true 2>/dev/null; do
  if [ $(($(date +%s) - t0)) -gt 300 ]; then echo "no SSH after 300 s" >&2; exit 1; fi
  if ! pgrep -x x05-netd >/dev/null; then echo "VM exited" >&2; exit 1; fi
  sleep 3
done
echo "ssh up after $(($(date +%s) - t0)) s"
