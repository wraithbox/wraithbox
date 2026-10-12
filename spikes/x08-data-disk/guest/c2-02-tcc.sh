# X08 boot 3: root got "Operation not permitted" listing the USB volume.
# Is it TCC (removable volumes), and does it hit every process? Throwaway.
set -u
H=/private/var/wbhomes
echo "root ls: $(ls -la $H 2>&1 | head -3)"
echo "root mkdir: $(mkdir $H/probe 2>&1 && echo ok)"
echo "root stat: $(stat -f '%u %g %Sp' $H 2>&1)"
echo "wbp-test ls: $(sudo -u wbp-test ls $H 2>&1 | head -1)"
echo "launchctl asuser 0 ls: $(launchctl asuser 0 ls $H 2>&1 | head -1)"
echo "/Volumes/WBData (internal) root ls: $(ls /Volumes/WBData 2>&1 | head -2 | tr '\n' ' ')"
log show --last 1m --style compact --predicate 'subsystem == "com.apple.TCC" OR process == "tccd"' 2>/dev/null | grep -iE 'removable|SystemPolicy|deny|x17' | tail -8
true
