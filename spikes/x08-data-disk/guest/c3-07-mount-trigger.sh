# X08 boot 6: what makes the System Policy deny the remount: a process of
# the user with its home under the mount point, or the keychain? Throwaway.
set -u
v=disk4s1
try() {
  diskutil unmount $v >/dev/null 2>&1
  diskutil mount -mountOptions nobrowse,nosuid,nodev,owners -mountPoint "$1" $v >/dev/null 2>&1 && r=mounted || r=DENIED
  echo "   remount on $1: $r"
}
diskutil mount -mountOptions nobrowse,nosuid,nodev,owners -mountPoint /private/var/x08h $v >/dev/null 2>&1
mkdir -p /private/var/x08h/wbpz; chown 699:20 /private/var/x08h/wbpz
echo "a: wbpz runs one command with HOME under the mount point"
sudo -u wbpz -H /bin/sh -c 'echo "   HOME=$HOME"; ls ~ >/dev/null'
try /private/var/x08h
echo "b: wbpz runs security (keychain search list in its home)"
sudo -u wbpz -H /bin/sh -c 'security list-keychains >/dev/null 2>&1; true'
try /private/var/x08h
echo "c: does launchd have a per-user domain for 621 and 699?"
launchctl print user/621 2>&1 | head -2
launchctl print user/699 2>&1 | head -2
diskutil unmount $v >/dev/null 2>&1
true
