#!/bin/sh
# For each ";; [name]" section of profiles/vmd.sb, boot a clone of the base
# bundle under the profile without that section, and record whether the VM
# boots to the guest's vsock listener.
# PROFILE=<file> picks another sectioned profile.
# Usage: ablate.sh <base bundle> <work dir for clones> <cache dir> [section...]
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
base=$1 work=$2 cache=$3
shift 3
sections=${*:-$(sed -nE 's/^;; \[([a-z-]+)\]$/\1/p' "${PROFILE:-$here/profiles/vmd.sb}")}
mkdir -p "$root/.scratch/ablate"
for s in $sections; do
  prof="$root/.scratch/ablate/without-$s.sb"
  awk -v drop="$s" '/^;; \[/{skip = ($0 == ";; [" drop "]")} !skip' "${PROFILE:-$here/profiles/vmd.sb}" >"$prof"
  b="$work/ablate-$s"
  rm -rf "$b"
  cp -c -R "$base" "$b"
  "$here/run.sh" "ablate-without-$s" --sb "$prof" -D BUNDLE="$b" -D CACHE="$cache" boot "$b" >/dev/null 2>&1
  printf '%s\t' "$s"
  grep -h '"step":"boot"\|"step":"error"\|"step":"sandbox_init"' "$here/results/ablate-without-$s.jsonl" | tail -1 | cut -c1-220
  rm -rf "$b"
done
