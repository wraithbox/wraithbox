#!/bin/sh
# Open the admin shell through the host-vmd's relay to port $2 and type the
# commands in file $3, then exit. Prints everything the shell sent back.
# Usage: shell.sh <ctl.sock> <port> <commands file>
set -eu
( printf 'relay %s\n' "$2"; sleep 2; cat "$3"; printf 'exit\n'; sleep 15 ) | nc -U "$1"
