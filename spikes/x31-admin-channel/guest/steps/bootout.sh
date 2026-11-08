launchctl bootout system/nl.wraithbox.guestd; echo bootout rc=$?; sleep 1; pgrep -lf libexec/wb-guestd || echo "no wb-guestd process"
