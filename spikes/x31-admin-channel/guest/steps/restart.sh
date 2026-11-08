# SIGKILL wb-guestroot 3 times, 15 s apart; time launchd's restart (poll 0.1 s, up to 20 s).
for r in 1 2 3; do
  old=$(pgrep -f libexec/wb-guestroot); kill -9 $old; i=0
  while [ $i -lt 200 ]; do n=$(pgrep -f libexec/wb-guestroot); [ -n "$n" ] && [ "$n" != "$old" ] && break; sleep 0.1; i=$((i+1)); done
  echo "round $r: killed $old, new '${n}' after ~$((i*100)) ms"
  sleep 15
done
launchctl print system/nl.wraithbox.guestroot | grep -E '^\s*(state|runs|pid) '
