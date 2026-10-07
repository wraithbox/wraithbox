#!/bin/sh
# X17-image-build: runs as the provisioning user's LaunchAgent at the first
# auto-login, and installs x17-guestd as a root LaunchDaemon with sudo.
# The host wrote this file, the payload, and the password file to the Data
# volume before the first boot. Throwaway.
d=/Users/Shared/x17
exec >>"$d/bootstrap.log" 2>&1
echo "bootstrap start $(date -u +%FT%TZ) $(id)"
if [ ! -f "$d/pw" ]; then echo "no password file"; exit 0; fi
sudo -S -p '' sh -c "
  mkdir -p /usr/local/libexec &&
  install -m 755 -o root -g wheel $d/x17-guestd /usr/local/libexec/x17-guestd &&
  install -m 644 -o root -g wheel $d/org.wraithbox.x17-guestd.plist /Library/LaunchDaemons/ &&
  launchctl bootstrap system /Library/LaunchDaemons/org.wraithbox.x17-guestd.plist &&
  echo installed" <"$d/pw"
echo "sudo exit $?"
rm -f "$d/pw"
echo "bootstrap end $(date -u +%FT%TZ)"
