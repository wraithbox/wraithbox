# X06-guest-xcode: run as root by x06-guestd. Create the project user x06p
# as wb-guestd would (S06-vm-lifecycle): a standard account, not in admin or
# wheel, no sudoers rule, never logged in at the console. Copy the test
# projects into its home as that user. Throwaway.
set -u
u=x06p
if ! id "$u" >/dev/null 2>&1; then
  sysadminctl -addUser "$u" -fullName "x06 project user" -shell /bin/zsh -password "$(openssl rand -hex 16)" -home /Users/"$u" 2>&1 | grep -v -- '-----'
  createhomedir -c -u "$u" 2>&1 | tail -1
fi
id "$u"
dsmemberutil checkmembership -U "$u" -G admin
sudo -H -u "$u" sh -c 'mkdir -p ~/work && ditto /Volumes/X06XCODE/x06/projects ~/work && ls -la ~/work'
who
launchctl print gui/$(id -u "$u") 2>&1 | head -3
launchctl print user/$(id -u "$u") 2>&1 | head -3
