# X08 boot 1: options or mount point? Each try starts unmounted. Throwaway.
set -u
v=disk5s1
st() { echo "   => $(mount | grep "^/dev/$v " || echo 'not mounted')"; diskutil unmount $v >/dev/null 2>&1; }
diskutil unmount $v >/dev/null 2>&1
echo "1 options, default point:"; diskutil mount -mountOptions nobrowse,nosuid,nodev $v 2>&1; st
echo "2 no options, /Volumes/wbhomes (left over dir):"; ls -lde@O /Volumes/wbhomes; diskutil mount -mountPoint /Volumes/wbhomes $v 2>&1; st
echo "3 fresh dir /Volumes/wbhomes2:"; mkdir /Volumes/wbhomes2; diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /Volumes/wbhomes2 $v 2>&1; st
echo "4 again, same dir:"; diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /Volumes/wbhomes2 $v 2>&1; st
echo "5 /private/var/wbhomes:"; mkdir -p /private/var/wbhomes; diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /private/var/wbhomes $v 2>&1; st
echo "6 again /private/var/wbhomes:"; diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /private/var/wbhomes $v 2>&1; st
echo "7 rmdir + mkdir /Volumes/wbhomes:"; rmdir /Volumes/wbhomes; mkdir /Volumes/wbhomes; diskutil mount -mountOptions nobrowse,nosuid,nodev -mountPoint /Volumes/wbhomes $v 2>&1; st
true
