#!/bin/sh
# Copy files to wbdev@guest:<dest>. Usage: scp.sh <scratch dir> <dest> <file>...
set -eu
scratch=$(cd "$1" && pwd)
dest=$2
shift 2
ip=$(cat "$scratch/guest-ip")
pwfile="$(cat "$scratch/bundle")/ssh-password"
printf '#!/bin/sh\ncat %s\n' "$pwfile" >"$scratch/askpass"
chmod 700 "$scratch/askpass"
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no -o LogLevel=ERROR "$@" "wbdev@$ip:$dest"
