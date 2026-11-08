#!/bin/sh
# Summarizes a netwatch.sh record: each process with a flow to a remote
# address, the remote endpoint and state, and in how many one-second
# samples it was seen. Listening and unconnected sockets are left out.
# Fields: 1 protocol, 2 time, 3 process or flow, 4 interface, 5 state.
# Usage: flows.sh <net.csv>
awk -F, '
  $3 !~ /<->/ { proc = $3; next }
  $3 ~ /<->\*/ { next }
  { split($3, a, "<->"); print $1, proc, a[2], $4, $5 }
' "$1" | sort | uniq -c | sort -rn
