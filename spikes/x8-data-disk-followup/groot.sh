#!/bin/sh
# groot.sh <bundle> <mac> <local script> [args...]: copy a script into the
# test guest and run it there as root through sudo, with the SSH user's
# password from the bundle. Throwaway spike helper for I228.
set -eu
bundle="$1"; mac="$2"; script="$3"
shift 3
here=$(dirname "$0")
pw="$HOME/Library/Caches/wraithbox-spikes/$bundle/ssh-password"
name=$(basename "$script")
"$here/gssh.sh" "$bundle" "$mac" --put "$script" "/tmp/$name"
"$here/gssh.sh" "$bundle" "$mac" "chmod +x /tmp/$name && sudo -S -p '' /tmp/$name $*" < "$pw"
