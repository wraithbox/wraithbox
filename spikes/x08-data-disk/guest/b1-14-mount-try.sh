# X08 boot 1: other ways to get the data volume mounted again. Throwaway.
set -u
echo "-- diskutil mount (default point):"; diskutil mount disk5s1 2>&1
echo "-- diskutil mountDisk disk5:"; diskutil mountDisk disk5 2>&1
echo "-- other mount point:"; mkdir -p /private/var/x08m; diskutil mount -mountPoint /private/var/x08m disk5s1 2>&1
echo "-- read-only:"; diskutil mount readOnly disk5s1 2>&1
mount | grep disk5 || echo "disk5 not mounted"
echo "-- kernel log, last 30 s:"
log show --last 30s --style compact --predicate 'process == "kernel"' 2>/dev/null | grep -iE 'apfs|mount|disk5|sandbox|deny' | tail -20
true
