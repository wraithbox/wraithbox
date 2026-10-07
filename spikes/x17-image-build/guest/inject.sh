#!/bin/sh
# X17-image-build: write files into a stopped, freshly installed bundle's Data
# volume from the host, as the user (no sudo). Everything written is owned by
# the host user's uid, because an unprivileged host process can't chown.
#
# Usage: inject.sh direct <bundle> <x17-guestd> <scratch>
#          LaunchDaemon plist + binary + /var/db/.AppleSetupDone, as the issue proposed
#        inject.sh agent <bundle> <x17-guestd> <scratch> <password file>
#          payload in /Users/Shared/x17 and a LaunchAgent that installs it with sudo
#          at the provisioning user's first auto-login; plus a host-written
#          LaunchDaemon that only touches a marker file, to see whether launchd runs it
# Throwaway.
set -eu
mode=$1 bundle=$2 guestd=$3 scratch=$4 passfile=${5:-}
here=$(cd "$(dirname "$0")" && pwd)
mnt="$scratch/datavol"
ts() { python3 -c 'import time; print(time.time())'; }
t0=$(ts)
whole=$("$here/datavol.sh" attach "$bundle/sys.img" "$mnt")
t1=$(ts)
m="$mnt"
case "$mode" in
direct)
  mkdir -p "$m/usr/local/libexec"
  cp "$guestd" "$m/usr/local/libexec/x17-guestd"
  chmod 755 "$m/usr/local/libexec/x17-guestd"
  cp "$here/org.wraithbox.x17-guestd.plist" "$m/Library/LaunchDaemons/"
  chmod 644 "$m/Library/LaunchDaemons/org.wraithbox.x17-guestd.plist"
  : >"$m/private/var/db/.AppleSetupDone"
  ;;
agent)
  d="$m/Users/Shared/x17"
  mkdir -p "$d"
  cp "$guestd" "$here/org.wraithbox.x17-guestd.plist" "$here/bootstrap.sh" "$d/"
  chmod 755 "$d/x17-guestd" "$d/bootstrap.sh"
  cp "$passfile" "$d/pw"
  chmod 600 "$d/pw"
  mkdir -p "$m/Library/LaunchAgents"
  cp "$here/org.wraithbox.x17-bootstrap.plist" "$m/Library/LaunchAgents/"
  chmod 644 "$m/Library/LaunchAgents/org.wraithbox.x17-bootstrap.plist"
  cat >"$m/Library/LaunchDaemons/org.wraithbox.x17-hostwritten.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>org.wraithbox.x17-hostwritten</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/touch</string>
		<string>/Users/Shared/x17/hostwritten-daemon-ran</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
EOF
  chmod 644 "$m/Library/LaunchDaemons/org.wraithbox.x17-hostwritten.plist"
  ;;
home)
  # After the provisioning boot: the same LaunchAgent, in the provisioning
  # user's own ~/Library/LaunchAgents. launchd accepts a per-user agent owned
  # by that user, and the files the host writes carry the host user's uid, so
  # this works only when that uid equals the guest user's uid (501 here).
  # <x17-guestd> and <password file> are unused; the agent payload is still in place.
  u=$(ls "$m/Users" | grep -v Shared | head -1)
  mkdir -p "$m/Users/$u/Library/LaunchAgents"
  cp "$here/org.wraithbox.x17-bootstrap.plist" "$m/Users/$u/Library/LaunchAgents/"
  chmod 644 "$m/Users/$u/Library/LaunchAgents/org.wraithbox.x17-bootstrap.plist"
  ls -lnd "$m/Users/$u" "$m/Users/$u/Library/LaunchAgents" "$m/Users/$u/Library/LaunchAgents/"* >&2
  ;;
*) echo "mode?" >&2; exit 2 ;;
esac
xattr -rc "$m/Users/Shared/x17" "$m/Library/LaunchDaemons" "$m/Library/LaunchAgents" "$m/usr/local" 2>/dev/null || true
find "$m/Users/Shared/x17" "$m/Library/LaunchDaemons" "$m/Library/LaunchAgents" "$m/usr/local" "$m/private/var/db/.AppleSetupDone" -newer "$bundle/cfg.json" -exec ls -lnd {} + >&2 2>/dev/null || true
t2=$(ts)
"$here/datavol.sh" detach "$mnt" "$whole"
t3=$(ts)
python3 -c "import json,sys; a=[float(x) for x in sys.argv[2:]]; print(json.dumps({'cmd':'inject','mode':sys.argv[1],'attachMount':a[1]-a[0],'write':a[2]-a[1],'detach':a[3]-a[2],'total':a[3]-a[0]}))" "$mode" "$t0" "$t1" "$t2" "$t3"
