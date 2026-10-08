# X06-guest-xcode: run as root, on the boot with a NAT network. Download the
# iOS simulator runtime with Xcode's own tool, with no Apple ID, and install
# it. Lists the hosts it reached. Throwaway.
set -u
diskutil list | awk '/X06XCODE/ {print $NF}' | xargs -n1 diskutil mount readOnly 2>/dev/null
t0=$(date +%s)
xcodebuild -downloadPlatform iOS -architectureVariant arm64 > /var/tmp/x06-download.log 2>&1; rc=$?
t1=$(date +%s)
tail -8 /var/tmp/x06-download.log
echo "RESULT downloadPlatform exit=$rc $((t1 - t0))s"
xcrun simctl runtime list 2>&1 | head -12
du -sh /Library/Developer/CoreSimulator/Volumes/* /Library/Developer/CoreSimulator/Cryptex/Images/* 2>/dev/null
log show --last 30m --style compact --predicate 'eventMessage CONTAINS "https://"' 2>/dev/null |
  grep -o 'https://[a-zA-Z0-9.-]*' | sort | uniq -c | sort -rn | head -20
