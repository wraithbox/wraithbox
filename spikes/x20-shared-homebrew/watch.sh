#!/bin/sh
# X20: stop the per-user source build in the guest when the host's free disk
# drops below a floor, or at a deadline, so the spike can't fill the
# maintainer's disk. Logs guest progress once a minute. Throwaway.
# watch.sh <socket> <project user> <log tag> <floor GiB> <deadline epoch>
S=$1 P=$2 TAG=$3 FLOOR=$4 DEADLINE=$5
here=$(dirname "$0")
while :; do
  free=$(df -g "$HOME" | awk 'NR==2 {print $4}')
  now=$(date +%s)
  prog=$(python3 "$here/req.py" "$S" runs "grep -cE '^==> (Pouring|Installing)' /private/tmp/x20-$TAG.log; grep -E '^==> Installing' /private/tmp/x20-$TAG.log | tail -1; df -g / | awk 'NR==2 {print \$4}'" 30 2>/dev/null | tail -3 | tr '\n' ' ')
  echo "$(date '+%H:%M:%S') hostFreeGiB=$free guest: $prog"
  if [ "$free" -lt "$FLOOR" ] || [ "$now" -ge "$DEADLINE" ]; then
    echo "$(date '+%H:%M:%S') stopping: hostFreeGiB=$free deadline=$DEADLINE"
    python3 "$here/req.py" "$S" runs "pkill -u $P; sleep 2; pkill -9 -u $P; echo killed" 30
    exit 0
  fi
  pgrep -f "req.py $S run .*bundle-per-user" >/dev/null || { echo "build request ended"; exit 0; }
  sleep 60
done
