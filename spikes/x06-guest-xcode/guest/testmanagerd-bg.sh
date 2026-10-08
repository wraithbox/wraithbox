# X06-guest-xcode: run as root. testmanagerd's LaunchAgent is limited to the
# LoginWindow and Aqua session types, so a Background session never has it.
# Load a copy without that limit into the project user's per-user domain
# (user/<uid>), to see whether xcodebuild test then runs there. Throwaway.
set -u
u=x06p
uid=$(id -u $u)
dir=/Library/Application\ Support/x06
mkdir -p "$dir"
p="$dir/com.apple.testmanagerd.plist"
cp /System/Library/LaunchAgents/com.apple.testmanagerd.plist "$p"
plutil -remove LimitLoadToSessionType "$p"
chown root:wheel "$p"; chmod 644 "$p"
launchctl bootout user/$uid/com.apple.testmanagerd 2>/dev/null
launchctl bootstrap user/$uid "$p"; echo "RESULT bootstrap user/$uid exit=$?"
launchctl print user/$uid/com.apple.testmanagerd 2>&1 | sed -n '1,12p'
# Second try: the same job under a label of our own.
plutil -replace Label -string org.wraithbox.x06.testmanagerd "$p"
launchctl bootstrap user/$uid "$p"; echo "RESULT bootstrap renamed exit=$?"
launchctl print user/$uid/org.wraithbox.x06.testmanagerd 2>&1 | sed -n '1,8p'
log show --last 2m --style compact --predicate 'process == "launchd" AND (eventMessage CONTAINS "testmanagerd" OR eventMessage CONTAINS "x06")' 2>/dev/null | tail -15
