# X06-guest-xcode: run as root. What S13-guest-confinement layer 1 does at a
# project's last session end: kill every process of the project user, in a
# loop until none remain. Does launchd start any of them again, with a
# booted simulator among them? Then the same after `launchctl bootout` of
# the user's domain. Throwaway.
u=x06p
uid=$(id -u $u)
echo "before: $(pgrep -U $u | wc -l) processes"
for i in 1 2 3 4 5 6 7 8 9 10; do
  n=$(pgrep -U $u | wc -l | tr -d ' ')
  [ "$n" -eq 0 ] && break
  pkill -KILL -U $u
  sleep 0.5
  echo "round $i: had $n"
done
echo "RESULT after kill loop: $(pgrep -U $u | wc -l | tr -d ' ') processes"
sleep 5
echo "RESULT 5s later: $(pgrep -U $u | wc -l | tr -d ' ') processes"
ps -U $u -o pid,ppid,command | cut -c1-120 | head -8
launchctl print user/$uid 2>&1 | grep -E "^\s+(active count|service count|session)" | head -3
launchctl bootout user/$uid 2>&1; echo "RESULT bootout user/$uid exit=$?"
sleep 2
echo "RESULT after bootout: $(pgrep -U $u | wc -l | tr -d ' ') processes"
launchctl print user/$uid 2>&1 | head -2
