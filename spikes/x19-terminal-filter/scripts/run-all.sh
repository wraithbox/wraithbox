#!/bin/sh
# usage: scripts/run-all.sh <scratch-dir>
# Records the session script once per terminal profile, plus the
# fullscreen renderer (CLAUDE_CODE_NO_FLICKER=1) in the Ghostty profile.
set -eu
spike=$(cd "$(dirname "$0")/.." && pwd)
scratch=$1
for p in ghostty apple iterm vscode; do
  rm -rf "$scratch/work-$p"
  "$spike/scripts/run-session.sh" "$p" "$scratch/work-$p" 2>"$scratch/log-$p.txt"
done
rm -rf "$scratch/work-fullscreen"
X19_EXTRA_ENV=CLAUDE_CODE_NO_FLICKER=1 \
  "$spike/scripts/run-session.sh" ghostty "$scratch/work-fullscreen" session-ghostty-fullscreen 2>"$scratch/log-fullscreen.txt"
