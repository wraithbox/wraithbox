#!/bin/sh
# Runs the signed I61 helper under one profile and records its output and the
# sandbox denials the kernel logged for it.
#   ./run.sh <profile-name> [extra i61 flags]
set -u
cd "$(dirname "$0")"
p=$1
shift
exe=work/X29.app/Contents/Helpers/i61.app/Contents/MacOS/i61
out=results/i61-$p.txt
start=$(date '+%Y-%m-%d %H:%M:%S')
"$exe" -mech pure -profile "profiles/$p.sb" -D "BUNDLE=$PWD/work/X29.app/Contents/Helpers/i61.app" -tag "$p" "$@" >"$out" 2>&1
echo "exit: $?" >>"$out"
sleep 2
{
	echo "--- sandbox denials (sandboxd violation reports for i61)"
	# On macOS 27 sandboxd reports the violations; the kernel's "Sandbox"
	# sender has none.
	/usr/bin/log show --style compact --start "$start" \
		--predicate 'eventMessage CONTAINS "Sandbox: i61("' 2>/dev/null |
		grep -o 'Sandbox: i61([0-9]*) deny.*' | sed -E 's/i61\([0-9]+\)/i61/' |
		sort | uniq -c | sort -rn
} >>"$out"
cat "$out"
