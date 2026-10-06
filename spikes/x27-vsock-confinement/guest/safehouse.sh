#!/bin/sh
# Render two test profiles from agent-safehouse v0.12.0 (commit 6bded066,
# Apache-2.0), the pin of S13-guest-confinement, with its own renderer
# (default selection, no optional features), then point HOME_DIR at
# /Users/proj1 and WORK_DIR at /Users/proj1/work for the guest:
#   s1-safehouse.sb             as upstream renders it
#   s2-safehouse-deny-vsock.sb  s1 plus the S13 rule, appended last
# Usage: safehouse.sh <safehouse checkout> <out dir>
set -eu
src=$1 out=$2
bash "$src/bin/safehouse.sh" --stdout |
  sed -e "s|$HOME|/Users/proj1|g" \
      -e "s|^(define HOME_DIR \".*\")|(define HOME_DIR \"/Users/proj1\")|" \
      -e "s|^(define WORK_DIR \".*\")|(define WORK_DIR \"/Users/proj1/work\")|" >"$out/s1-safehouse.sb"
{
  cat "$out/s1-safehouse.sb"
  printf '\n;; S13-guest-confinement: no AF_VSOCK for project users\n(deny system-socket (socket-domain AF_VSOCK))\n'
} >"$out/s2-safehouse-deny-vsock.sb"
