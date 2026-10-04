#!/bin/sh
# usage: scripts/run-session.sh <profile> <workdir> [name] [script]
# Records a script with the installed claude. Extra environment for
# claude goes in X19_EXTRA_ENV (comma-separated K=V).
set -eu
spike=$(cd "$(dirname "$0")/.." && pwd)
profile=$1
work=$2
name=${3:-session-$profile}
script=${4:-$spike/scripts/session.txt}
mkdir -p "$work"
cd "$work"
[ -d .git ] || { git init -q && echo "# x19 scratch" > README.md; }
"$spike/bin/record" -o "$spike/recordings/$name.cast" -profile "$profile" \
  -script "$script" -screens "$spike/screens/$name" \
  -- claude --model haiku
