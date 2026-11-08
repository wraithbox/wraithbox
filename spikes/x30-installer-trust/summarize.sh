#!/bin/sh
# Writes <res>/<case>.summary.txt from a run-case.sh record: the result
# lines, the TSS (Apple signing server) requests and responses, and every
# message that reports a failure, a mismatch or a refusal. The full log
# stays out of git (hundreds of MB). Usage: summarize.sh <res> <case>
set -u
here=$(cd "$(dirname "$0")" && pwd)
res=$1 c=$2
out=$res/$c.summary.txt
{
  echo "== exit: $(cat "$res/$c.rc" 2>/dev/null) start $(cat "$res/$c.start") end $(cat "$res/$c.end" 2>/dev/null)"
  echo "== result lines"; cat "$res/$c.jsonl"
  echo "== stderr (last 5)"; tail -5 "$res/$c.err"
  echo "== TSS requests and HTTP responses"
  python3 -I "$here/logfind.py" "$res/$c.log.ndjson" 'tss_submit|summary for|personalizing "KernelCache"|personalizing "SystemVolume"|ticket' 260
  echo "== failures"
  python3 -I "$here/logfind.py" "$res/$c.log.ndjson" 'fail|error|mismatch|invalid|refus|denied|digest|hash' 400 |
    grep -v -E 'FindProperty|not part of manifest|personalize later|HTTP Request Header|error=NoError|NoSuchRecord' | head -150
  echo "== host flows (1 s samples)"; "$here/flows.sh" "$res/$c.net.csv" | grep -i 'apple\.Virtu' || echo "(none sampled)"
} >"$out" 2>&1
wc -l "$out"
