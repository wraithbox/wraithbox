# X17-image-build: the sealing scan's account and remote-access checks, run as
# root in the guest by x17-guestd before the image is sealed. Prints one
# FAIL line per finding and exits 1 if there is any, so a build stops
# (fail closed). Throwaway; the product scan is wb-guestd code.
fail=0
f() { echo "FAIL [$1] $2"; fail=$((fail + 1)); }
ok() { echo "ok   [$1] $2"; }

# Accounts: no account with a password other than root in admin or wheel,
# and no non-system account in a base image (project users come later),
# except one: macOS refuses to delete the volume's last secure token holder,
# so the provisioning user may stay if it is the volume owner, disabled, in
# no group that grants anything, and without a shell.
owners=$(diskutil apfs listCryptoUsers / 2>/dev/null | awk '/^\+-- /{g=$2} /Volume Owner: Yes/{print g}')
disabled_owner() {
  u=$1
  guid=$(dscl . -read /Users/"$u" GeneratedUID 2>/dev/null | awk '{print $2}')
  echo "$owners" | grep -qx "$guid" || return 1
  pwpolicy -u "$u" -authentication-allowed 2>&1 | grep -q 'is not allowed to authenticate' || return 1
  [ "$(dscl . -read /Users/"$u" UserShell | awk '{print $2}')" = /usr/bin/false ] || return 1
  for g in $(id -Gn "$u"); do
    # Groups every local user is in (some through nested groups), and the
    # one pwpolicy adds when it disables an account.
    case "$g" in staff | everyone | localaccounts | _lpoperator | com.apple.sharepoint.group.* | com.apple.access_disabled) ;; *) return 1 ;; esac
  done
  return 0
}
for g in admin wheel; do
  for u in $(dscl . -read /Groups/$g GroupMembership 2>/dev/null | sed 's/^GroupMembership://'); do
    [ "$u" = root ] && continue
    # macOS ships _mbsetupuser in admin, with no password.
    aa=$(dscl . -read /Users/"$u" AuthenticationAuthority 2>/dev/null)
    case "$aa" in *ShadowHash*) f account "account $u with a password is in $g" ;; esac
  done
done
kept=""
for u in $(dscl . -list /Users UniqueID | awk '$2>=500 {print $1}'); do
  if [ -z "$kept" ] && disabled_owner "$u"; then
    kept=$u
    ok account "kept $u: volume owner, disabled, no groups, no shell (macOS refuses to delete the last secure token holder)"
  else
    f account "non-system user $u ($(dscl . -read /Users/"$u" UniqueID | awk '{print $2}'))"
  fi
done
for d in /Users/*; do
  case "$d" in /Users/Shared | "/Users/$kept") ;; *) f account "home directory $d" ;; esac
done
[ $fail = 0 ] && ok account "no admin or wheel account with a password, no other non-system user"
kuid=""
[ -n "$kept" ] && kuid=$(id -u "$kept")

# Remote Login: sshd disabled in launchd, not loaded, nothing on port 22.
before=$fail
launchctl print-disabled system | grep -q '"com.openssh.sshd" => enabled' && f remote-login "com.openssh.sshd is enabled"
launchctl print system/com.openssh.sshd >/dev/null 2>&1 && f remote-login "com.openssh.sshd is loaded"
netstat -anp tcp | awk '$6=="LISTEN" && $4 ~ /[.:]22$/' | grep -q . && f remote-login "a process listens on TCP 22"
extra=$(ls -A /var/root/.ssh 2>/dev/null; ls -A /etc/ssh/sshd_config.d 2>/dev/null | grep -v '^100-macos.conf$')
[ -n "$extra" ] && f remote-login "files in /var/root/.ssh or sshd_config.d beyond what macOS ships: $(echo $extra)"
[ $fail = $before ] && ok remote-login "sshd disabled, not loaded, no listener on 22"

# Other remote access a provisioning step could leave on.
before=$fail
for s in com.apple.screensharing com.apple.RemoteDesktop.PrivilegeProxy; do
  launchctl print-disabled system | grep -q "\"$s\" => enabled" && f remote-access "$s is enabled"
done
[ $fail = $before ] && ok remote-access "screen sharing and remote management off"

# Login: no automatic login, and no kcpassword (it holds a password).
before=$fail
defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser >/dev/null 2>&1 && f login "autoLoginUser is set"
[ -e /etc/kcpassword ] && f login "/etc/kcpassword exists"
[ $fail = $before ] && ok login "no automatic login"

# sudo: only what macOS ships.
before=$fail
[ -n "$(ls -A /etc/sudoers.d 2>/dev/null)" ] && f sudoers "/etc/sudoers.d is not empty: $(ls -A /etc/sudoers.d | tr '\n' ' ')"
grep -v '^[[:space:]]*#' /etc/sudoers | grep -q NOPASSWD && f sudoers "NOPASSWD in /etc/sudoers"
[ $fail = $before ] && ok sudoers "no sudoers additions"

# Leftovers of the build: files owned by a non-system uid outside /Users
# (anything the host wrote carries the host user's uid), the agent payload,
# and agents or daemons that are not Apple's or wb-guestd's. The kept
# account's per-user temp folders and message cache may stay.
before=$fail
left=$(find /System/Volumes/Data -xdev \( -path /System/Volumes/Data/Users -prune \) -o \( -uid +499 -print \) 2>/dev/null |
  while IFS= read -r p; do
    if [ -n "$kuid" ] && [ "$(stat -f %u "$p")" = "$kuid" ]; then
      case "$p" in
        (/System/Volumes/Data/private/var/folders/*) continue ;;
        ("/System/Volumes/Data/private/var/db/mds/messages/$kuid" | "/System/Volumes/Data/private/var/db/mds/messages/$kuid/"*) continue ;;
      esac
    fi
    echo "$p"
  done | head -20)
[ -n "$left" ] && f leftovers "owned by a non-system uid: $(echo $left)"
[ -e /Users/Shared/x17 ] && f leftovers "/Users/Shared/x17 (agent payload) exists"
for p in /Library/LaunchAgents/* /Library/LaunchDaemons/*; do
  [ -e "$p" ] || continue
  case "$p" in
    /Library/LaunchDaemons/org.wraithbox.x17-guestd.plist | /Library/LaunchDaemons/com.apple.* | /Library/LaunchDaemons/amsdstat.plist) ;;
    *) f leftovers "launchd job $p" ;;
  esac
done
[ $fail = $before ] && ok leftovers "no host-owned files, payload or extra launchd jobs"

# Secrets (the SEC04-no-guest-secrets part of the scan): keys, history, keychains.
before=$fail
for p in /var/root/.ssh/id_* /var/root/.*history /Users/*/.ssh/id_* /Users/*/.*history /Users/*/Library/Keychains/*; do
  [ -e "$p" ] && f secrets "$p"
done
[ $fail = $before ] && ok secrets "no keys, shell history or user keychains"

echo "scan result: $([ $fail = 0 ] && echo PASS || echo FAIL)"
[ $fail = 0 ]
