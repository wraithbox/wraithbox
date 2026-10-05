#!/bin/sh
# X05-fs-benchmark spike: run bench.mjs in the guest, on the data disk, and
# append its JSON lines to results/<name>-bench.jsonl.
# Usage: gbench.sh <scratch dir> <name> <runs> [bench flags]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
scratch=$1 name=$2 runs=$3
shift 3
"$here/gssh.sh" "$scratch" -scp "$here/bench.mjs" /Users/x05/bench.mjs
"$here/gssh.sh" "$scratch" /Users/x05/tools/node/bin/node /Users/x05/bench.mjs \
  /Users/x05/tools /Volumes/data/work.noindex "$name" "$runs" "$@" \
  | tee -a "$here/results/$name-bench.jsonl"
