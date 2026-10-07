# X17-image-build: undo the provisioning, run as root in the guest by
# x17-guestd: no automatic login, Remote Login off, provisioning user and
# everything it owns removed. mkreq.py prepends P='<provisioning password>'.
# Throwaway.
u=$(dscl . -list /Users UniqueID | awk '$2>=500 {print $1}' | head -1)
uid=$(dscl . -read /Users/"$u" UniqueID | awk '{print $2}')
guid=$(dscl . -read /Users/"$u" GeneratedUID | awk '{print $2}')

# Remote Login off, whether or not it was on.
launchctl bootout system/com.openssh.sshd 2>&1
launchctl disable system/com.openssh.sshd
# No automatic login.
defaults delete /Library/Preferences/com.apple.loginwindow autoLoginUser 2>&1
rm -f /etc/kcpassword

# The provisioning user is the volume's only secure token holder and volume
# owner, and macOS refuses to delete the last one. Take the token away first.
sysadminctl -secureTokenStatus "$u" 2>&1
sysadminctl -secureTokenOff "$u" -password "$P" -adminUser "$u" -adminPassword "$P" 2>&1
sysadminctl -secureTokenStatus "$u" 2>&1
diskutil apfs listCryptoUsers / 2>&1
sysadminctl -deleteUser "$u" -adminUser "$u" -adminPassword "$P" 2>&1
if dscl . -read /Users/"$u" >/dev/null 2>&1; then sysadminctl -deleteUser "$u" 2>&1; fi
if dscl . -read /Users/"$u" >/dev/null 2>&1; then dscl . -delete /Users/"$u" 2>&1; fi
if dscl . -read /Users/"$u" >/dev/null 2>&1; then
  echo "RESULT user $u could not be deleted"
else
  echo "RESULT user $u deleted"
fi
diskutil apfs listCryptoUsers / 2>&1

# Its home has "everyone deny delete" ACLs, which bind root too.
[ -d /Users/"$u" ] && chmod -R -N /Users/"$u" && chflags -R nouchg,noschg /Users/"$u" 2>/dev/null
rm -rf /Users/"$u"
# Everything else it owned: per-user temp and cache folders, Spotlight, and
# the desktop picture cache keyed on its GeneratedUID.
find /System/Volumes/Data -xdev -user "$uid" -prune -exec rm -rf {} + 2>&1 | head -5
rm -rf "/Library/Caches/Desktop Pictures/$guid"
find /System/Volumes/Data -xdev -user "$uid" 2>/dev/null | head -5
true
