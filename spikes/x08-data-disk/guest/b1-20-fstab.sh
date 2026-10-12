# X08 boot 1: before the reboot of g1, put an fstab entry for the data
# volume by label, so Disk Arbitration mounts it at boot with nosuid and
# nodev at the users' home path. An image could carry this line, because
# the label is the same in every VM, while the volume UUID is not. Throwaway.
set -u
echo 'LABEL=WBData /Volumes/wbhomes apfs rw,nobrowse,nosuid,nodev' > /etc/fstab
chmod 644 /etc/fstab
cat /etc/fstab
sync
