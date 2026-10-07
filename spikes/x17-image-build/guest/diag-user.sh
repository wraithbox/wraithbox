# X17-image-build: why the provisioning user can't be removed. Run as root by
# x17-guestd. Throwaway.
set -x
sysadminctl -secureTokenStatus wbprov 2>&1
diskutil apfs listCryptoUsers / 2>&1
fdesetup list 2>&1
dscl . -read /Users/wbprov UniqueID UserShell AuthenticationAuthority 2>&1 | head -20
dscl . -read /Users/_mbsetupuser UniqueID UserShell Password AuthenticationAuthority 2>&1
ls -lOe /Users/ /Users/wbprov 2>&1 | head -30
ls -lOe /var/db/dslocal/nodes/Default/users/wbprov.plist 2>&1
csrutil status
# Is it TCC? A root LaunchDaemon without Full Disk Access.
touch /Users/wbprov/Music/x17-probe 2>&1; ls /Users/wbprov/Library 2>&1 | head -3
sqlite3 "/Library/Application Support/com.apple.TCC/TCC.db" 'select service,client,auth_value from access' 2>&1 | head
true
