#!/bin/sh
# setuid-probe.sh: run as root in the test guest. Compares X08's C
# probe (/tmp/euid) with a copy of the conformance test binary
# (/tmp/datamount.test) as setuid-root files in /private/var/tmp, where
# setuid counts. Throwaway spike helper for I228.
d=/private/var/tmp
cp /tmp/euid $d/p-c
cp /tmp/datamount.test $d/p-go
chown root:wheel $d/p-c $d/p-go
chmod 4755 $d/p-c $d/p-go
ls -l $d/p-c $d/p-go
echo "C as nobody:"; sudo -u nobody $d/p-c
echo "Go as nobody:"; sudo -u nobody env WB_DATAMOUNT_PROBE=1 $d/p-go
echo "Go as wbdev:"; sudo -u wbdev env WB_DATAMOUNT_PROBE=1 $d/p-go
codesign -dv $d/p-go 2>&1 | grep -E "flags|Signature"
codesign -dv $d/p-c 2>&1 | grep -E "flags|Signature"
rm -f $d/p-c $d/p-go
