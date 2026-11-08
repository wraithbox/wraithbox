#!/bin/sh
# Makes each case's image, installs from it with run-case.sh, then removes
# the image, one case at a time, so at most one changed image is on disk.
# The bundle stays for a look at how far the install got.
# Usage: run-all.sh <x30 binary> <src.ipsw> <spike cache dir x30> <results dir> case...
set -u
here=$(cd "$(dirname "$0")" && pwd)
x30=$1 src=$2 dir=$3 res=$4
shift 4
for c in "$@"; do
  [ -f "$dir/$c.ipsw" ] || "$here/make-images.sh" "$src" "$dir" "$c" || exit 1
  cp "$dir/$c.changes.jsonl" "$res/$c.changes.jsonl"
  "$here/run-case.sh" "$x30" "$c" "$dir/$c.ipsw" "$dir/$c" "$res"
  rm -f "$dir/$c.ipsw"
done
