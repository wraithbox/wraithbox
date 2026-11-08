#!/bin/sh
# Run a script file in the guest as wbdev, or as root through sudo with -r.
# Usage: ssh.sh <scratch dir> [-r] <script file> [args...]
# <scratch>/guest-ip holds the address, <scratch>/bundle the bundle path
# (its ssh-password is wbdev's password, written by vm:provision).
set -eu
scratch=$(cd "$1" && pwd)
shift
root=""
if [ "$1" = -r ]; then root=1; shift; fi
script=$1
shift
ip=$(cat "$scratch/guest-ip")
pwfile="$(cat "$scratch/bundle")/ssh-password"
printf '#!/bin/sh\ncat %s\n' "$pwfile" >"$scratch/askpass"
chmod 700 "$scratch/askpass"
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no -o LogLevel=ERROR"
b64=$(base64 <"$script" | tr -d '\n')
if [ -n "$root" ]; then
  ssh $sshopts "wbdev@$ip" "echo $b64 | base64 -d >/tmp/x31-step.sh && sudo -S -p '' sh /tmp/x31-step.sh $*" <"$pwfile"
else
  ssh $sshopts "wbdev@$ip" "echo $b64 | base64 -d >/tmp/x31-step.sh && sh /tmp/x31-step.sh $*" </dev/null
fi
