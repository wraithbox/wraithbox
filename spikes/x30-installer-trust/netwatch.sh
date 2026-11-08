#!/bin/sh
# Records every TCP and UDP flow on the host once a second, with the
# process that owns it, until killed. nettop needs no root and shows all
# users' processes. The output is nettop's CSV with the process line kept
# in front of its flows, so each flow can be attributed.
# Usage: netwatch.sh <out file>
set -u
out=$1
: >"$out"
while :; do
  for m in tcp udp; do
    nettop -m "$m" -n -L 1 -x 2>/dev/null | awk -v m="$m" 'NR>1 { print m "," $0 }' >>"$out"
  done
  sleep 1
done
