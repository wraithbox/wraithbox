# X17-image-build: can x17-guestd (a root LaunchDaemon without Full Disk
# Access) create and remove a project user and its home, on an image whose
# only secure token holder is the disabled provisioning user? Throwaway.
u=wbp-test
sysadminctl -addUser "$u" -fullName "x17 project test" -shell /bin/zsh -password "$(openssl rand -hex 16)" -home /Users/"$u" 2>&1 | grep -v -- '-----'
createhomedir -c -u "$u" 2>&1 | tail -2
id "$u"
sysadminctl -secureTokenStatus "$u" 2>&1
ls -la /Users/"$u" | head -5
# Write in the protected folders as the user would.
sudo -u "$u" sh -c "echo x > /Users/$u/Documents/f && echo x > /Users/$u/Desktop/f" 2>&1
sysadminctl -deleteUser "$u" 2>&1 | grep -v -- '-----'
dscl . -read /Users/"$u" >/dev/null 2>&1 && echo "RESULT user still there" || echo "RESULT user deleted"
[ -e /Users/"$u" ] && echo "RESULT home still there: $(ls -A /Users/$u | tr '\n' ' ')" || echo "RESULT home removed"
true
