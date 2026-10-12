#!/bin/sh
# X08: prepend a host-state header to each guest script, so one request
# carries both. Writes .scratch/<tag>-<script>.sh. Throwaway.
# Usage: assemble.sh <tag> <host-state file>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out="$here/../../.scratch"
for s in mount2 mkusers fill2 check snap; do
  cat "$2" "$here/guest/$s.sh" > "$out/$1-$s.sh"
done
ls "$out" | grep "^$1-"
