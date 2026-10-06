#!/bin/sh
# Find which denial makes a state saved unconfined fail to restore under
# the profile: for each extra rule, restore a fresh clone of the saved
# bundle under <profile> plus that one rule.
# Usage: addon.sh <profile> <saved bundle> <work dir> <cache dir>
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
prof=$1 src=$2 work=$3 cache=$4
i=0
while IFS= read -r rule; do
  i=$((i + 1))
  p="$root/.scratch/addon-$i.sb"
  cat "$prof" >"$p"
  echo "$rule" >>"$p"
  b="$work/addon-$i"
  rm -rf "$b"
  cp -c -R "$src" "$b"
  "$here/run.sh" "addon-$i" --sb "$p" -D BUNDLE="$b" -D CACHE="$cache" restore "$b" >/dev/null 2>&1
  printf '%s\t' "$rule"
  if grep -q '"restoreError"' "$here/results/addon-$i.jsonl"; then echo "restore fails"
  elif grep -q '"vsockSeconds":[0-9]' "$here/results/addon-$i.jsonl"; then echo "restores"
  else echo "other: $(grep -h '"step":"restore"\|"step":"error"' "$here/results/addon-$i.jsonl" | cut -c1-160)"; fi
  rm -rf "$b"
done <<'EOF'
(allow sysctl-read (sysctl-name "machdep.cpu.brand_string"))
(allow sysctl-read (sysctl-name "kern.maxfilesperproc"))
EOF
