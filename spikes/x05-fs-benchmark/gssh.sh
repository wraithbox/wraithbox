#!/bin/sh
# X05-fs-benchmark spike (copied from X03-network-path): run a command in the guest over SSH, through
# x05-netd's host-only debug forward (127.0.0.1:2222 -> guest:22).
# The password is the one x05 run --provision set, in .scratch/guest-pass.
# Usage: gssh.sh <scratch dir> <command...>   (gssh.sh <scratch> -scp <src> <dst>)
set -eu
scratch=$(cd "$1" && pwd)
shift
cat >"$scratch/askpass" <<EOF
#!/bin/sh
cat "$scratch/guest-pass"
EOF
chmod 700 "$scratch/askpass"
export SSH_ASKPASS="$scratch/askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=none
opts="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=10 -o PreferredAuthentications=password,keyboard-interactive -o PubkeyAuthentication=no"
if [ "$1" = "-scp" ]; then
  shift
  exec scp $opts -P 2222 "$1" "x05@127.0.0.1:$2"
fi
exec ssh $opts -p 2222 x05@127.0.0.1 "$@"
