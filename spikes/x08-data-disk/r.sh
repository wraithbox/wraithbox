#!/bin/sh
# X08-data-disk: run one guest script as root through x17-guestd and log it.
# Usage: r.sh <results-file-stem> <label> <guest script> [timeout]
# Throwaway.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
sock="$(cd "$here/../.." && pwd)/.scratch/g.sock"
stem=$1 label=$2 script=$3 to=${4:-600}
python3 -I "$here/req.py" "$sock" run --script "$script" --timeout "$to" \
  --log "$here/results/$stem.jsonl" --label "$label"
