# Approach step 3. As proj1 (a standard user, standing in for a project
# user): bind wb-guestroot's port while launchd restarts it, bind it while
# it is booted out, bind a port above 1024, and connect to it from inside.
P=/usr/local/bin/x31-probe
cd /tmp
echo "--- wb-guestd back after the crash loop"; sleep 1; launchctl print system/nl.wraithbox.guestd | grep -E '^\s*(state|runs|pid) ' | head -3
echo "--- a: proj1 squats 1022 for 8 s while root kills wb-guestroot (KeepAlive restarts it)"
sudo -u proj1 $P squat 1022 8 > /tmp/sq-a.out 2>&1 &
sleep 1; old=$(pgrep -f libexec/wb-guestroot); kill -9 $old; echo "killed wb-guestroot $old"
wait; cat /tmp/sq-a.out; sleep 1; echo "wb-guestroot now: $(pgrep -f libexec/wb-guestroot)"; tail -n 2 /var/log/wb-guestroot.log | cut -c1-150
echo "--- b: proj1 squats 1022 for 5 s while wb-guestroot is booted out"
launchctl bootout system/nl.wraithbox.guestroot; echo bootout rc=$?
sudo -u proj1 $P squat 1022 5
echo "--- c: root squats 1022 for 1 s while booted out (control)"
$P squat 1022 1 & sleep 2; kill %1 2>/dev/null; wait
launchctl bootstrap system /Library/LaunchDaemons/nl.wraithbox.guestroot.plist; echo bootstrap rc=$?; sleep 2; pgrep -lf libexec/wb-guestroot
echo "--- d: proj1 binds 5022 (above 1024, control)"
sudo -u proj1 $P squat 5022 1 & sleep 2; kill %1 2>/dev/null; wait
echo "--- e: proj1 and root connect to 1022 from inside the guest"
for cid in 1 2 3 4294967295; do sudo -u proj1 $P connect $cid 1022; done
for cid in 1 2 3; do $P connect $cid 1022; done
sysctl -a 2>/dev/null | grep -i vsock | head -5
tail -n 4 /var/log/wb-guestroot.log | cut -c1-200
