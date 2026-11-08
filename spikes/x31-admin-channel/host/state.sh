#!/bin/sh
# Run one guest state step and open the admin shell in that state.
# Usage: state.sh <scratch> <name> <step script> [args]
set -u
X=$1; name=$2; step=$3; shift 3
S=$(dirname "$0")/..
{
  echo "=== $name: step"; sh $S/guest/ssh.sh $X -r $step "$@"
  echo "=== $name: connect 1023 (wb-guestd)"; printf 'connect 1023\n' | nc -U $X/ctl.sock -w 5 | head -1
  echo "=== $name: connect 1022 (guestroot)"; printf 'connect 1022\n' | nc -U $X/ctl.sock -w 5 | head -1
  echo "=== $name: admin shell"; sh $S/host/shell.sh $X/ctl.sock 1022 $S/host/cmds-id.txt 2>&1 | tr -d '\r' | sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g' | grep -v 'root@Wraiths'
} 2>&1 | tee $X/state-$name.out
