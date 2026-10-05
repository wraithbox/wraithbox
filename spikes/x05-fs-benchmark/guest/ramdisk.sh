#!/bin/sh
# X05-fs-benchmark spike, in the guest: the CPU control. Copies the
# workloads to an APFS volume on a guest RAM disk and runs bench.mjs there,
# so the guest's time without any virtual disk I/O is known.
# Usage: ramdisk.sh <label> <runs>
set -eu
dev=$(hdiutil attach -nomount ram://6291456 2>/dev/null | awk '{print $1}')   # 3 GiB
diskutil erasevolume APFS ram "$dev" >&2
R=/Volumes/ram
mdutil -i off "$R" >/dev/null 2>&1 || true
mkdir -p "$R/work.noindex"
for d in npm repo gobuild; do
  cp -R "/Volumes/data/work.noindex/$d" "$R/work.noindex/"
done
rm -rf "$R/work.noindex/npm/proj/node_modules"
df -h "$R" >&2
/Users/x05/tools/node/bin/node /Users/x05/bench.mjs /Users/x05/tools "$R/work.noindex" "$1" "$2" \
  --only npm-ci,git-status,go-incr,go-clean
diskutil eject "$dev" >&2
