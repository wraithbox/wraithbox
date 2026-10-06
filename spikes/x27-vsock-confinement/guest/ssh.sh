#!/bin/sh
# Run a command in the guest as the admin user x27 (or as root with -r).
# Usage: ssh.sh <scratch dir> [-r] <command>
set -eu
scratch=$1
shift
ip=$(cat "$scratch/guest-ip")
pass=$(cat "$scratch/guest-pass")
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
sshopts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no -o LogLevel=ERROR"
if [ "$1" = -r ]; then
  shift
  b64=$(printf '%s\n' "$*" | base64 | tr -d '\n')
  ssh $sshopts "x27@$ip" "echo '$pass' | sudo -S -p '' sh -c \"\$(echo $b64 | base64 -d)\""
else
  ssh $sshopts "x27@$ip" "$*"
fi
