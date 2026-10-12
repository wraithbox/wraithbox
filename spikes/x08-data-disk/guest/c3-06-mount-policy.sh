# X08 boot 6: the kernel's System Policy denies file-mount on
# /private/var/wbhomes once users have homes under it. Is that the rule,
# and does mount -u (changing flags in place) get past it? Throwaway.
set -u
v=disk4s1
echo "1 a fresh dir, then a user record whose home is under it, then mount:"
mkdir -p /private/var/x08h
dscl . -create /Users/wbpz GeneratedUID $(uuidgen); dscl . -create /Users/wbpz UniqueID 699; dscl . -create /Users/wbpz PrimaryGroupID 20
dscl . -create /Users/wbpz NFSHomeDirectory /private/var/x08h/wbpz
diskutil mount -mountOptions nobrowse,nosuid,nodev,owners -mountPoint /private/var/x08h $v 2>&1
mount | grep "^/dev/$v " || echo "   not mounted"
diskutil unmount $v >/dev/null 2>&1
echo "2 a fresh dir with no home under it:"
mkdir -p /private/var/x08n
diskutil mount -mountOptions nobrowse,owners -mountPoint /private/var/x08n $v 2>&1
mount | grep "^/dev/$v "
echo "3 mount -u to add nosuid,nodev in place:"
mount -u -o nosuid,nodev,nobrowse /private/var/x08n 2>&1; echo "   exit $?"
mount | grep "^/dev/$v "
diskutil unmount $v >/dev/null 2>&1
log show --style compact --last 15s --predicate 'process == "kernel"' 2>/dev/null | grep 'file-mount' | tail -4
true
