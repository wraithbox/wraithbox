# Approach step 3, second part: restart timing under a proj1 squat, and
# connects from inside with a 3 s cap each (a connect to CID 3 hung).
P=/usr/local/bin/x31-probe
cd /tmp
pkill -f x31-probe; echo "leftover probes killed"
cap() { "$@" & p=$!; i=0; while kill -0 $p 2>/dev/null && [ $i -lt 30 ]; do sleep 0.1; i=$((i+1)); done; if kill -0 $p 2>/dev/null; then kill $p; echo "{\"ev\":\"hung >3s\",\"cmd\":\"$*\"}"; fi; wait $p 2>/dev/null; }
echo "--- a2: proj1 squats 1022 for 15 s; root kills wb-guestroot at 1 s; pgrep every 0.5 s"
sudo -u proj1 $P squat 1022 15 > /tmp/sq-a2.out 2>&1 &
sleep 1; old=$(pgrep -f libexec/wb-guestroot); kill -9 $old; t0=$(date +%s); echo "killed wb-guestroot $old"
for i in $(seq 1 28); do n=$(pgrep -f libexec/wb-guestroot); [ -n "$n" ] && { echo "new wb-guestroot $n after $(( $(date +%s)-t0 )) s"; break; }; sleep 0.5; done
wait; cat /tmp/sq-a2.out
launchctl print system/nl.wraithbox.guestroot | grep -E '^\s*(state|runs|pid|last terminating) '
tail -n 3 /var/log/wb-guestroot.log | cut -c1-150
echo "--- e: connects from inside the guest to 1022 (proj1, then root)"
for cid in 1 2 3 4294967295; do cap sudo -u proj1 $P connect $cid 1022; done
for cid in 1 2 3; do cap $P connect $cid 1022; done
pkill -f x31-probe
tail -n 3 /var/log/wb-guestroot.log | cut -c1-200
