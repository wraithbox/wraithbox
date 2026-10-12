# X08 boot 1: why the planted setuid id printed nothing. Throwaway.
set -u
H=/Volumes/wbhomes
cp /usr/bin/id $H/wbp2/plain-id; chown wbp2:wbp2 $H/wbp2/plain-id; chmod 755 $H/wbp2/plain-id
sudo -u wbp2 /bin/sh -c '/Volumes/wbhomes/wbp2/plain-id -u; echo "plain copy exit $?"; /Volumes/wbhomes/wbp2/suid-id -u; echo "suid copy exit $?"' 2>&1
/Volumes/wbhomes/wbp2/suid-id -u; echo "suid copy as root exit $?"
codesign -dv /Volumes/wbhomes/wbp2/suid-id 2>&1 | head -4
echo "perl setuid probe:"; ls -l /usr/bin/perl
log show --last 2m --style compact --predicate 'eventMessage CONTAINS "suid-id" OR eventMessage CONTAINS "setuid"' 2>/dev/null | tail -8
true
