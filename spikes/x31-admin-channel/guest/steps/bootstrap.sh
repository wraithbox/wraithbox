launchctl bootstrap system /Library/LaunchDaemons/nl.wraithbox.guestd.plist; echo bootstrap rc=$?; sleep 2; pgrep -lf libexec/wb-guestd
