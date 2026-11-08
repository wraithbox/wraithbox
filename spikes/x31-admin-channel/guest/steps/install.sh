# As root, in the provisioning boot: install wb-guestd 0.0.0-x31.1 and
# x31-guestroot 0.0.0-x31.1 (as wb-guestroot) as two root LaunchDaemons
# from /Library/WraithBox/libexec, the allow files wb-guestd needs
# (host-work.md, "Guests that run wb-guestd"), the probe and race tools,
# and the standard user proj1 that stands in for a project user.
# Files were copied to /tmp/x31 first.
set -e
T=/tmp/x31
install -d -m 755 -o root -g wheel /Library/WraithBox /Library/WraithBox/libexec
install -m 755 -o root -g wheel $T/wb-guestd.1 /Library/WraithBox/libexec/wb-guestd
install -m 755 -o root -g wheel $T/x31-guestroot.1 /Library/WraithBox/libexec/wb-guestroot
install -m 644 -o root -g wheel $T/nl.wraithbox.guestd.plist $T/nl.wraithbox.guestroot.plist /Library/LaunchDaemons/
printf 'root\n' > $T/allow
for f in cron.allow at.allow; do
  install -m 0644 -o root -g wheel $T/allow /private/var/at/$f
  cmp $T/allow /private/var/at/$f
done
install -d -m 755 -o root -g wheel /usr/local/bin
install -m 755 -o root -g wheel $T/x31-probe $T/x31-race /usr/local/bin/
sysadminctl -addUser proj1 -fullName "project user" -password "$(openssl rand -hex 12)"
id proj1
dseditgroup -o checkmember -m proj1 admin || true
launchctl bootstrap system /Library/LaunchDaemons/nl.wraithbox.guestd.plist
launchctl bootstrap system /Library/LaunchDaemons/nl.wraithbox.guestroot.plist
sleep 3
echo "--- wb-guestd.log"; cat /var/log/wb-guestd.log
echo "--- wb-guestroot.log"; cat /var/log/wb-guestroot.log
ls -la /Library/WraithBox/libexec
