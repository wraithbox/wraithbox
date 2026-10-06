#!/bin/sh
# Boot one bundle under a profile once per extra device, each pointing
# outside the bundle, and record whether the VM starts and boots to vsock.
# A profile of "none" runs unconfined, as the control.
# Usage: devices.sh <tag> <profile> <bundle> <cache dir> <outside dir> <nat mac>
set -u
here=$(cd "$(dirname "$0")" && pwd)
tag=$1 prof=$2 b=$3 cache=$4 out=$5 mac=$6
for dev in nat share disk serial spice mic; do
  case $dev in
    nat) opt="--nat $mac" ;;
    share) opt="--share $out/share" ;;
    disk) opt="--disk $out/secret.img" ;;
    serial) opt="--serial $out/serial.log" ;;
    spice) opt="--spice" ;;
    mic) opt="--mic" ;;
  esac
  # shellcheck disable=SC2086 # opt is two words on purpose
  if [ "$prof" = none ]; then
    "$here/run.sh" "$tag-$dev" boot "$b" $opt >/dev/null 2>&1
  else
    "$here/run.sh" "$tag-$dev" --sb "$prof" -D BUNDLE="$b" -D CACHE="$cache" boot "$b" $opt >/dev/null 2>&1
  fi
  printf '%s\t' "$dev"
  grep -h '"step":"boot"' "$here/results/$tag-$dev.jsonl" | cut -c1-260
done
