#!/bin/sh
# X05-fs-benchmark spike: does a data image shrink on the host when the
# guest deletes data? Writes 1 GiB in the guest, then deletes it, and
# prints the image's allocated size on the host after each step.
# Usage: trim.sh <scratch dir> <image>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
s=$1 img=$2
alloc() { echo "$1: $(du -k "$img" | cut -f1) KiB allocated, guest $("$here/gssh.sh" "$s" df -k /Volumes/data | tail -1 | awk '{print $3}') KiB used"; }
alloc start
"$here/gssh.sh" "$s" /Users/x05/tools/node/bin/node -e "'const fs=require(\"fs\");const fd=fs.openSync(\"/Volumes/data/trim.bin\",\"w\");const b=Buffer.alloc(1<<20,7);for(let i=0;i<1024;i++)fs.writeSync(fd,b);fs.fsyncSync(fd);fs.closeSync(fd)'"
sleep 5
alloc "after writing 1 GiB"
"$here/gssh.sh" "$s" rm /Volumes/data/trim.bin
"$here/gssh.sh" "$s" sync
sleep 30
alloc "30 s after deleting it"
