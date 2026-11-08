launchctl print system/nl.wraithbox.guestd | grep -E '^\s*(state|runs|pid|last exit|last terminating)' ; cat /var/root/x31-crashloop.log | tail -3; tail -n 3 /var/log/wb-guestd.log | cut -c1-160
