#!/bin/sh
# remount-probe.sh: run as root in the test guest after the data mount.
# Shows what `mount -u -o <one option>` does to the other flags of the
# data volume's mount, and runs the setuid probes on the data volume
# with nosuid. Throwaway spike helper for I228.
h=/private/var/wbhomes
show() { echo "$1: $(mount | grep " on $h ")"; }
show "now"
mount -u -o nosuid,nodev,nobrowse $h; show "after -o nosuid,nodev,nobrowse"
mount -u -o suid $h; show "after -o suid"
mount -u -o nosuid $h; show "after -o nosuid"
mount -u -o nosuid,nodev,nobrowse $h; show "after -o nosuid,nodev,nobrowse"
d=$h/probe-dir
mkdir -p $d; chmod 755 $d
cp /tmp/euid $d/p-c; cp /tmp/datamount.test $d/p-go
chown root:wheel $d/p-c $d/p-go; chmod 4755 $d/p-c $d/p-go
echo "on the data volume, nosuid:"
echo "C as nobody: $(sudo -u nobody $d/p-c)"
echo "Go as nobody: $(sudo -u nobody env WB_DATAMOUNT_PROBE=1 $d/p-go)"
mount -u -o suid,nodev,nobrowse $h; show "after -o suid,nodev,nobrowse"
echo "on the data volume, suid:"
echo "C as nobody: $(sudo -u nobody $d/p-c)"
echo "Go as nobody: $(sudo -u nobody env WB_DATAMOUNT_PROBE=1 $d/p-go)"
mount -u -o nosuid,nodev,nobrowse $h; show "restored"
rm -rf $d
