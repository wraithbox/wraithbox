# X06-guest-xcode: run as root by x06-guestd. Install full Xcode from the
# read-only spike disk the way an image build would through wb-guestd, with
# no Apple ID and no GUI: copy, select, accept the license, first launch.
# Throwaway.
set -u
t0=$(date +%s)
if [ ! -d /Applications/Xcode.app ]; then
  ditto /Volumes/X06XCODE/Xcode.app /Applications/Xcode.app || exit 1
fi
chown -R root:wheel /Applications/Xcode.app
t1=$(date +%s); echo "RESULT copy: $((t1 - t0))s"
xcode-select -s /Applications/Xcode.app/Contents/Developer
xcode-select -p
xcodebuild -license accept; echo "license accept exit $?"
xcodebuild -runFirstLaunch 2>&1 | tail -5; echo "runFirstLaunch exit $?"
t2=$(date +%s); echo "RESULT first launch: $((t2 - t1))s"
xcodebuild -version
xcodebuild -checkFirstLaunchStatus; echo "checkFirstLaunchStatus exit $?"
xcodebuild -showsdks 2>&1 | grep -- -sdk
xcrun simctl runtime list 2>&1 | head -20
du -sh /Applications/Xcode.app
