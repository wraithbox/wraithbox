# X06-guest-xcode: run as root. Where testmanagerd and CoreSimulator are
# defined, and which launchd session types they are limited to. Throwaway.
for f in /System/Library/LaunchAgents/com.apple.testmanagerd.plist /System/Library/LaunchDaemons/com.apple.testmanagerd.plist \
         /System/Library/LaunchAgents/com.apple.CoreSimulator.CoreSimulatorService.plist \
         /Library/LaunchAgents/*.plist /Library/LaunchDaemons/*.plist; do
  [ -e "$f" ] || continue
  echo "== $f"
  plutil -p "$f" | grep -E -A3 "Label|LimitLoadToSessionType|MachServices|Program\"" | head -30
done
ls /System/Library/LaunchAgents | grep -i -E "testmanager|simulator|xcode|dt\."
ls /System/Library/LaunchDaemons | grep -i -E "testmanager|simulator|xcode|dt\."
find /Applications/Xcode.app/Contents -name "*.plist" -path "*Launch*" 2>/dev/null | head
find /Library/Developer -maxdepth 3 2>/dev/null | head -30
