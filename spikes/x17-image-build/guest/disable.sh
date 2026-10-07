# X17-image-build: macOS refuses to delete the provisioning user, because it
# is the volume's only secure token holder, so disable it instead: out of
# every group that grants anything, a password nobody keeps, disabled by
# account policy (isDisabled), no shell, hidden, and its keychain removed.
# mkreq.py prepends P='<provisioning password>'. Run as root by x17-guestd.
# Throwaway.
u=$(dscl . -list /Users UniqueID | awk '$2>=500 {print $1}' | head -1)
for g in $(id -Gn "$u"); do
  case "$g" in staff|everyone|localaccounts) continue ;; esac
  dseditgroup -o edit -d "$u" -t user "$g" 2>&1
  # Some memberships are only GroupMembership or GroupMembers entries.
  dscl . -delete /Groups/"$g" GroupMembership "$u" 2>/dev/null
  dscl . -delete /Groups/"$g" GroupMembers "$(dscl . -read /Users/"$u" GeneratedUID | awk '{print $2}')" 2>/dev/null
done
id "$u"
dscl . -read /Groups/admin GroupMembership
# A random password, discarded at once. sysadminctl with the old password
# keeps the secure token usable, so macOS can still unlock the volume key.
R=$(openssl rand -hex 32)
sysadminctl -resetPasswordFor "$u" -newPassword "$R" -adminUser "$u" -adminPassword "$P" 2>&1
R=
pwpolicy -u "$u" -disableuser 2>&1
dscl . -create /Users/"$u" UserShell /usr/bin/false
dscl . -create /Users/"$u" IsHidden 1
dscl . -read /Users/"$u" AuthenticationAuthority UserShell IsHidden
sysadminctl -secureTokenStatus "$u" 2>&1
# Home: the login keychain and whatever else root may remove.
ls -la /Users/"$u"/Library/Keychains 2>&1
rm -rf /Users/"$u"/Library/Keychains/* 2>&1
find /Users/"$u" -mindepth 1 -maxdepth 2 2>&1 | head -40
true
