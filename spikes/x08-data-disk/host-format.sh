#!/bin/sh
# X08 cycle 3: the host makes a blank data disk with an APFS volume on it,
# before any guest has seen it. On the host the raw file is a disk image,
# not internal storage, so its APFS volume isn't encrypted at rest, and no
# key of the host's or a guest's Secure Enclave is needed to mount it.
# The host never attaches it again after a guest has written to it.
# As the user, no sudo (X17-image-build attached a raw image the same way).
# Usage: host-format.sh <new raw image> <volume name>   Throwaway.
set -eu
img=$1 name=$2
[ ! -e "$img" ] || { echo "$img exists" >&2; exit 1; }
mkfile -n 20g "$img"
out=$(hdiutil attach -nomount -imagekey diskimage-class=CRawDiskImage "$img")
dev=$(echo "$out" | awk 'NR==1{print $1}')
echo "attached as $dev"
# Refuse anything but the image just attached: eraseDisk on a real disk
# would destroy it.
info=$(diskutil info "$dev")
echo "$info" | grep -E 'Device / Media Name|Protocol|Disk Size'
echo "$info" | grep -q 'Protocol: *Disk Image' || { echo "not a disk image, stop" >&2; hdiutil detach "$dev"; exit 1; }
echo "$info" | grep -q 'Disk Size: *21.5 GB' || { echo "unexpected size, stop" >&2; hdiutil detach "$dev"; exit 1; }
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
diskutil eraseDisk APFS "$name" GPT "$dev"
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME host format $(echo "$t1 - $t0" | bc) s"
diskutil info "$name" | grep -E 'Volume UUID|FileVault|Encrypt|Owners|Device Location'
diskutil apfs list | grep -A12 "Name: *$name" | grep -E 'FileVault|Encrypt'
hdiutil detach "$dev"
ls -ls "$img"
