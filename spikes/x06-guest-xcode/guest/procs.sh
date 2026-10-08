# X06-guest-xcode: run as root. The project user's processes: parent,
# process group, POSIX session id (from ps -j), terminal, and for the
# CoreSimulator ones the launchd domain they run in. Throwaway.
u=x06p
echo "processes of $u: $(pgrep -U $u | wc -l)"
ps -U $u -o pid,ppid,pgid,sess,tty,command | cut -c1-150 | sort -k2 -n | head -40
for p in $(pgrep -U $u -f 'CoreSimulatorService|launchd_sim' | head -3); do
  echo "== procinfo $p"
  launchctl procinfo "$p" 2>/dev/null | grep -E "domain|session|program path|responsible" | head -8
done
