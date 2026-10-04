#!/bin/sh
# usage: scripts/run-copy.sh <scratch-dir>
# Records /copy with a stub pbcopy first in PATH.
set -eu
spike=$(cd "$(dirname "$0")/.." && pwd)
scratch=$1
PATH="$spike/scripts/fakebin:$PATH" X19_PBCOPY_OUT="$scratch/pbcopy.out" \
  "$spike/scripts/run-session.sh" ghostty "$scratch/work-copy" copy-ghostty "$spike/scripts/copy.txt"
echo "pbcopy stub received: $(cat "$scratch/pbcopy.out")"
