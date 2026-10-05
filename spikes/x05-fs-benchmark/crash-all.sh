#!/bin/sh
# X05-fs-benchmark spike: the kill-mid-write runs after the first one.
# Usage: crash-all.sh <scratch dir> <bundle> <raw image>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
s=$1 b=$2 img=$3
"$here/crash.sh" "$s" kill-raw-cached-none-client "$b" "$img" cached none client 20
"$here/crash.sh" "$s" kill-raw-automatic-full-service "$b" "$img" automatic full service 20
"$here/crash.sh" "$s" kill-raw-automatic-none-service "$b" "$img" automatic none service 20
"$here/crash.sh" "$s" kill-raw-cached-none-service2 "$b" "$img" cached none service 5
"$here/crash.sh" "$s" kill-raw-cached-none-service3 "$b" "$img" cached none service 45
