#!/bin/sh
# Make the host files the device tests point at, outside the VM bundle and
# so outside what the wb-vmd profile allows: a directory with a secret file
# (for a shared directory), a raw disk image that starts with a marker (for
# an extra disk), and the path of a serial port log.
# Usage: outside.sh <dir>
set -eu
d=$1
mkdir -p "$d/share"
stamp=$(date +%s)
echo "x25 host secret $stamp" >"$d/share/secret.txt"
dd if=/dev/zero of="$d/secret.img" bs=1m count=4 2>/dev/null
printf 'X25-DISK-MARKER-%s' "$stamp" >"$d/marker"
dd if="$d/marker" of="$d/secret.img" conv=notrunc 2>/dev/null
rm -f "$d/serial.log"
ls -la "$d" "$d/share"
