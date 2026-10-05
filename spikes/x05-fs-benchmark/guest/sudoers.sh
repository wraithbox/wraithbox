#!/bin/sh
# X05-fs-benchmark spike, in the guest: passwordless sudo for the spike
# user, so the benchmark can run `purge` and the disk setup. Reads the
# password once from stdin.
set -eu
read -r p
printf '%s\n' "$p" | sudo -S -p "" sh -c 'echo "x05 ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/x05; chmod 440 /etc/sudoers.d/x05'
sudo -n cat /etc/sudoers.d/x05
