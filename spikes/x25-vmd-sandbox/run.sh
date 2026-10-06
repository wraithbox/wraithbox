#!/bin/sh
# Run one x25 command, keep its stdout (JSON lines) and stderr, and the
# Sandbox log lines it caused.
# Usage: run.sh <name> <x25 args...>
# Writes results/<name>.jsonl, results/<name>.err, results/<name>.sandbox.txt
# (distinct operations) and .scratch/<name>.sandbox.log (raw, not kept).
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
name=$1
shift
start=$(date '+%Y-%m-%d %H:%M:%S')
"$root/.scratch/x25" "$@" >"$here/results/$name.jsonl" 2>"$here/results/$name.err"
rc=$?
sleep 2
end=$(date '+%Y-%m-%d %H:%M:%S')
echo "{\"step\":\"run\",\"name\":\"$name\",\"exit\":$rc,\"start\":\"$start\",\"end\":\"$end\"}" >>"$here/results/$name.jsonl"
"$here/sblog.sh" "$start" "$end" "$root/.scratch/$name.sandbox.log" "$here/results/$name.sandbox.txt" >/dev/null
cat "$here/results/$name.jsonl"
exit $rc
