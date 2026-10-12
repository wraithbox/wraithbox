# X08 boot 1: the setuid probe with and without nosuid, at a mount point
# outside /Volumes (b1-15: /Volumes/wbhomes can't be mounted on again).
# Throwaway.
set -u
v=disk5s1; mp=/private/var/wbhomes
diskutil unmount $v >/dev/null 2>&1
diskutil mount -mountOptions nobrowse -mountPoint $mp $v
mount | grep "^/dev/$v "
echo "without nosuid: $(sudo -u wbp2 $mp/wbp2/suid-euid)"
diskutil unmount $v >/dev/null 2>&1
diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint $mp $v
mount | grep "^/dev/$v "
echo "with nosuid: $(sudo -u wbp2 $mp/wbp2/suid-euid)"
