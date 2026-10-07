# X17-image-build: what the guest looks like, run as root by x17-guestd. Throwaway.
echo "== sw_vers"; sw_vers
echo "== uptime"; uptime
echo "== users uid>=500"; dscl . -list /Users UniqueID | awk '$2>=500'
echo "== admin"; dscl . -read /Groups/admin GroupMembership
echo "== wheel"; dscl . -read /Groups/wheel GroupMembership
echo "== remote login"; systemsetup -getremotelogin 2>&1
echo "== sshd disabled?"; launchctl print-disabled system | grep -i ssh
echo "== sshd job"; launchctl print system/com.openssh.sshd 2>&1 | head -3
echo "== listening tcp"; netstat -anp tcp | grep LISTEN
echo "== console user"; stat -f %Su /dev/console
echo "== who"; who
echo "== setup assistant / login processes"; ps -axo user,pid,etime,comm | grep -E -i "setup|loginwindow|sshd|x17|Finder|Dock$" | grep -v grep
echo "== .AppleSetupDone"; ls -ln /var/db/.AppleSetupDone 2>&1
echo "== injected files"; ls -lnd /usr/local/libexec/x17-guestd /Library/LaunchDaemons/org.wraithbox.* /Library/LaunchAgents/org.wraithbox.* /Users/Shared/x17 /Users/Shared/x17/* 2>&1
echo "== bootstrap log"; cat /Users/Shared/x17/bootstrap.log 2>&1
echo "== hostwritten daemon"; launchctl print system/org.wraithbox.x17-hostwritten 2>&1 | head -5
echo "== guestd job"; launchctl print system/org.wraithbox.x17-guestd 2>&1 | grep -E "state|pid|path" | head -5
echo "== guestd log"; cat /var/log/x17-guestd.log 2>&1 | tail -5
echo "== FileVault"; fdesetup status
echo "== SIP"; csrutil status
echo "== sudoers.d"; ls -la /etc/sudoers.d 2>&1
