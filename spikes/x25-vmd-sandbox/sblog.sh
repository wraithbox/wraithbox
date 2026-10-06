#!/bin/sh
# Collect the Sandbox log lines about x25 between two times, and reduce
# them to one line per distinct (action, operation, target).
# Usage: sblog.sh <start "YYYY-mm-dd HH:MM:SS"> <end> <raw out> <summary out>
set -eu
log show --start "$1" --end "$2" --predicate 'eventMessage CONTAINS "x25("' --style compact >"$3" 2>/dev/null || true
# Lines look like: ... Sandbox: x25(1234) allow file-read-data /path
#              or: ... Sandbox: x25(1234) deny(1) file-read-data /path
sed -nE 's/.*Sandbox: x25\([0-9]+\) (allow|deny)(\([0-9]+\))? ([a-z0-9*-]+) ?(.*)$/\1 \3 \4/p' "$3" | sort | uniq -c | sort -k2 >"$4"
wc -l "$3" "$4"
