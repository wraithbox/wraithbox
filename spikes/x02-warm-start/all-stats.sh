#!/bin/sh
# X02-warm-start: p50/p95 for every series in results/. Run from this directory.
set -eu
for f in cold-4g cold-8g cold-clone-4g; do
  echo "## $f"
  python3 stats.py "results/$f.jsonl" cold netSeconds,dhcpSeconds
done
for f in cycle-4g cycle-8g; do
  echo "## $f"
  python3 stats.py "results/$f.jsonl" cycle pauseMs,saveSeconds,stateBytes,stateAllocBytes,stopMs,configMs,restoreSeconds,resumeMs,netSeconds
done
for f in series-4g series-8g; do
  echo "## $f"
  python3 stats.py "results/$f.jsonl" restore configMs,restoreSeconds,resumeMs,netSeconds
done
