#!/bin/bash
# Narrows down the in-process slowdown: in-process with each profile.
# Usage: bench2.sh [rounds] profile...
cd "$(dirname "$0")" || exit 1
rounds=$1
shift
for i in $(seq "$rounds"); do
  for p in "$@"; do
    ./bin/harness -mech pure -netd bin/netd-nocgo -profile "$p" 2>/dev/null | grep RESULT | sed -E "s|RESULT (mech=[^ ]+).*throughput=([^ ]+).*|\1 $p \2|"
  done
done
