# X06-guest-xcode: run as root by x17-guestd. Mount the read-only spike disk,
# put x06-guestd in place of x17-guestd's binary (same LaunchDaemon plist),
# and restart the daemon. Throwaway.
set -u
v=$(diskutil list | awk '/X06XCODE/ {print $NF}')
echo "volume device: $v"
mount | grep -q X06XCODE || diskutil mount readOnly "$v"
mount | grep X06XCODE
ls /Volumes/X06XCODE/x06
install -o root -g wheel -m 0755 /Volumes/X06XCODE/x06/x06-guestd /usr/local/libexec/x17-guestd.new
mv /usr/local/libexec/x17-guestd.new /usr/local/libexec/x17-guestd
sw_vers
(sleep 1; launchctl kickstart -k system/org.wraithbox.x17-guestd) >/dev/null 2>&1 &
echo swapped
