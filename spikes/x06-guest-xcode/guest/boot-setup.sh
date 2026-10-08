# X06-guest-xcode: run as root after each boot. Mount the spike disk
# read-only (profiles.sh reads it). The clock is set by a separate request
# with the host's time. Throwaway.
v=$(diskutil list | awk '/X06XCODE/ {print $NF}')
mount | grep -q X06XCODE || diskutil mount readOnly "$v"
mount | grep X06XCODE
who
launchctl print gui/503 2>&1 | head -1
