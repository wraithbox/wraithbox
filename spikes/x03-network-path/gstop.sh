#!/bin/sh
# X03-network-path spike: shut the guest down over SSH and wait for run.sh's
# x03-netd to exit. Usage: gstop.sh <scratch dir>
here=$(cd "$(dirname "$0")" && pwd)
"$here/gssh.sh" "$1" 'cat > /tmp/p; sudo -S -p "" shutdown -h now < /tmp/p; rm /tmp/p' <"$1/guest-pass" >/dev/null 2>&1
while pgrep -x x03-netd >/dev/null; do sleep 1; done
echo stopped
