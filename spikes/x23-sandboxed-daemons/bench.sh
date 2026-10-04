#!/bin/bash
# Runs the netd harness N times per mechanism, interleaved, and prints the
# RESULT lines. Usage: bench.sh [rounds] [MiB per stream]
cd "$(dirname "$0")" || exit 1
rounds=${1:-5}
mb=${2:-64}
for i in $(seq "$rounds"); do
  ./bin/harness -mb "$mb" -mech none -netd bin/netd-nocgo -profile profiles/netd.sb 2>/dev/null | grep RESULT
  ./bin/harness -mb "$mb" -mech pure -netd bin/netd-nocgo -profile profiles/netd.sb 2>/dev/null | grep RESULT
  ./bin/harness -mb "$mb" -mech pure -netd bin/netd-nocgo -profile profiles/allow-all.sb 2>/dev/null | grep RESULT | sed 's/mech=pure/mech=pure-allowall/'
  ./bin/harness -mb "$mb" -mech cgo -netd bin/netd -profile profiles/netd.sb 2>/dev/null | grep RESULT
  ./bin/harness -mb "$mb" -mech exec -netd bin/netd-nocgo -profile profiles/netd-exec.sb 2>/dev/null | grep RESULT
done
