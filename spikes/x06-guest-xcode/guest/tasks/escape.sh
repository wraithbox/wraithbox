# X06 task, run under the Seatbelt profile: does work handed to the
# simulator leave the profile? /Users/Shared is writable by every user and
# outside the profile's write grants. The simulator runtime has no shell,
# so the write goes through its `defaults` tool. Throwaway.
rm -f /Users/Shared/x06-direct.plist /Users/Shared/x06-via-sim.plist 2>/dev/null
defaults write /Users/Shared/x06-direct k v 2>&1; echo "RESULT direct write exit=$?"
dev=$(xcrun simctl list devices booted 2>/dev/null | awk -F'[()]' '/Booted/ {print $2; exit}')
echo "booted device: $dev"
xcrun simctl spawn "$dev" defaults write /Users/Shared/x06-via-sim k v 2>&1; echo "RESULT write via simctl spawn exit=$?"
ls -la /Users/Shared/ 2>&1 | grep x06
stat -f '%Su %N' /Users/Shared/x06-via-sim.plist 2>&1
