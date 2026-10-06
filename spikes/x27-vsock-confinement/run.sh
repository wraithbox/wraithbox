#!/bin/sh
# X27-vsock-confinement experiments, against the VM that guest/setup.sh left
# running. Throwaway. Writes JSON lines into <results dir>.
#
# Usage: run.sh <scratch dir> <results dir> <experiment>...
#   push      copy guest/profiles/*.sb into the guest again
#   matrix    probe matrix as root, the admin user x27, and proj1, with
#             x27-guestd running and then stopped
#   profiles  probe matrix as proj1 under each installed Seatbelt profile, x27-guestd
#             stopped (so only the profile stands in the way)
#   gap       kill -9 x27-guestd five times while the host connects every
#             5 ms, to see how long its port is free
#   squat     proj1 squats port 1024 while root kills x27-guestd; the host
#             connects every 50 ms and records who answers
#   squat-sb  the same with proj1 under p2-deny-socket-domain-40.sb
set -eu
scratch=$1 out=$2
shift 2
mkdir -p "$out"
ip=$(cat "$scratch/guest-ip")
pass=$(cat "$scratch/guest-pass")
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no -o LogLevel=ERROR"
g() { ssh $sshopts "x27@$ip" "$@"; }
asroot() { g "echo '$pass' | sudo -S -p '' sh -c '$1'"; }
asproj() { g "echo '$pass' | sudo -S -p '' -u proj1 $1"; }
ctl() { (printf '%s\n' "$1"; sleep "$2") | nc -U "$scratch/ctl.sock"; }
P=/usr/local/bin/x27-probe
S=/usr/local/share/x27
D=system/org.wraithbox.x27-guestd
guestd_pid() { g "pgrep -x x27-guestd"; }
guestd_up() { asroot "launchctl bootstrap system /Library/LaunchDaemons/org.wraithbox.x27-guestd.plist" || true; sleep 1; }
guestd_down() { asroot "launchctl bootout $D" || true; sleep 1; }

for exp in "$@"; do
  case $exp in
  matrix)
    guestd_up
    ctl "connect 1024" 2 >"$out/matrix-host-sanity.jsonl"
    asroot "$P matrix root-guestd-running" >"$out/matrix.jsonl"
    g "$P matrix admin-guestd-running" >>"$out/matrix.jsonl"
    asproj "$P matrix proj1-guestd-running" >>"$out/matrix.jsonl"
    guestd_down
    asproj "$P matrix proj1-guestd-stopped" >>"$out/matrix.jsonl"
    g "$P matrix admin-guestd-stopped" >>"$out/matrix.jsonl"
    asroot "$P matrix root-guestd-stopped" >>"$out/matrix.jsonl"
    guestd_up
    ;;
  push)
    # copy the profiles in guest/profiles to the guest again
    g "rm -rf /tmp/x27p && mkdir -p /tmp/x27p"
    scp $sshopts "$(dirname "$0")"/guest/profiles/*.sb "x27@$ip:/tmp/x27p/"
    asroot "rm -f $S/*.sb && install -m 644 -o root -g wheel /tmp/x27p/*.sb $S/"
    ;;
  profiles)
    guestd_down
    : >"$out/profiles.jsonl"
    for p in $(g "cd $S && ls *.sb | sed 's/[.]sb\$//'"); do
      asproj "sandbox-exec -f $S/$p.sb $P matrix $p" >>"$out/profiles.jsonl" 2>>"$out/profiles.err" || echo "{\"label\":\"$p\",\"sandbox-exec\":\"exit $?\"}" >>"$out/profiles.jsonl"
    done
    guestd_up
    ;;
  gap)
    guestd_up
    : >"$out/gap-host.jsonl"
    : >"$out/gap-kills.txt"
    for i in 1 2 3 4 5; do
      sleep 12  # past launchd's 10 s throttle, so each kill is a first crash
      ctl "poll 1024 5 6000" 7 >>"$out/gap-host.jsonl" &
      sleep 2
      pid=$(guestd_pid)
      echo "run $i kill pid $pid at $(date +%s)" >>"$out/gap-kills.txt"
      asroot "kill -9 $pid"
      wait
    done
    asroot "cat /var/log/x27-guestd.log" >"$out/gap-guestd.log"
    ;;
  squat | squat-sb)
    guestd_up
    sleep 12
    if [ "$exp" = squat ]; then cmd="$P squat 1024 25 proj1"; else cmd="sandbox-exec -f $S/p2-deny-socket-domain-40.sb $P squat 1024 25 proj1-sb"; fi
    asproj "$cmd" >"$out/$exp-guest.jsonl" 2>&1 &
    sp=$!
    ctl "poll 1024 50 40000" 41 >"$out/$exp-host.jsonl" &
    hp=$!
    sleep 3
    pid=$(guestd_pid)
    echo "kill pid $pid at $(date +%s)" >"$out/$exp-kills.txt"
    asroot "kill -9 $pid"
    wait $sp || true
    wait $hp || true
    asroot "launchctl print $D | grep -E \"state|pid|runs|last exit\"" >>"$out/$exp-kills.txt" || true
    asroot "cat /var/log/x27-guestd.log" >"$out/$exp-guestd.log"
    ;;
  *) echo "unknown experiment $exp" >&2; exit 2 ;;
  esac
done
