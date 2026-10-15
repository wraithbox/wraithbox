#!/bin/bash
# Greedy minimization of the TLS rules: starts from the X23 wide TLS rules,
# split into one rule per name or path, and drops each rule whose removal
# still lets the TLS probe pass. Run twice for a stable result.
#   ./minimize.sh <base.sb> <atoms.txt>   -> prints the rules it kept
set -u
cd "$(dirname "$0")"
base=$1
atoms=$2
exe=work/X29.app/Contents/Helpers/i61.app/Contents/MacOS/i61
keep=()
while IFS= read -r l; do keep+=("$l"); done <"$atoms" # bash 3.2: no mapfile
try() { # $@: rules
	{ cat "$base"; printf '%s\n' "$@"; } >work/try.sb
	"$exe" -mech pure -profile work/try.sb -only tls >/dev/null 2>&1
}
try "${keep[@]}" || { echo "full set fails TLS" >&2; exit 1; }
i=0
while [ $i -lt ${#keep[@]} ]; do
	without=("${keep[@]:0:$i}" "${keep[@]:$((i + 1))}")
	if try "${without[@]}" && try "${without[@]}"; then
		echo "drop: ${keep[$i]}" >&2
		keep=("${without[@]}")
	else
		echo "keep: ${keep[$i]}" >&2
		i=$((i + 1))
	fi
done
printf '%s\n' "${keep[@]}"
