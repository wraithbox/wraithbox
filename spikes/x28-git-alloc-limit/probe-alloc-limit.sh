#!/bin/sh
# X28: the start check proposed for wb-hostd. Does this git honor
# GIT_ALLOC_LIMIT? Runs hash-object on 2 KiB of zeros with a 1 KiB limit
# (expected: exit 128 and "over limit"), without the variable (expected:
# exit 0), with GIT_ALLOC_LIMIT=0 (unlimited in git), and with a value git
# can't parse. Writes results/x28-probe.txt.
cd "$(dirname "$0")" || exit 1
mkdir -p results
{
  date -u +%Y-%m-%dT%H:%M:%SZ
  for g in /opt/homebrew/bin/git /usr/bin/git; do
    echo "== $g: $("$g" version)"
    for lim in 1k unset 0 garbage; do
      if [ "$lim" = unset ]; then set --; else set -- "GIT_ALLOC_LIMIT=$lim"; fi
      out=$(head -c 2048 /dev/zero | env -i PATH=/usr/bin:/bin HOME=/nonexistent LC_ALL=C \
        GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null "$@" "$g" hash-object --stdin 2>&1)
      echo "GIT_ALLOC_LIMIT=$lim exit=$? output=$out"
    done
  done
} > results/x28-probe.txt
cat results/x28-probe.txt
