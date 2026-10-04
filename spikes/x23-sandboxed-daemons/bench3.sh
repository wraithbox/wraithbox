#!/bin/bash
# Bare (deny default) with an explicit GOMAXPROCS before confining, against
# netd.sb (hw.ncpu allowed). Usage: bench3.sh [rounds]
cd "$(dirname "$0")" || exit 1
for i in $(seq "${1:-3}"); do
  ./bin/harness -setprocs -mech pure -netd bin/netd-nocgo -profile profiles/deny-all.sb 2>/dev/null | grep RESULT | sed 's/mech=pure/mech=pure-denyall-setprocs/'
  ./bin/harness -mech pure -netd bin/netd-nocgo -profile profiles/netd.sb 2>/dev/null | grep RESULT | sed 's/mech=pure/mech=pure-netd.sb/'
done
