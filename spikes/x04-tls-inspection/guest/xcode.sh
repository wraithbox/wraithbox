# X04: unpack the host's Xcode.app (a tar on the read-only share; ditto from
# the virtio-fs share failed on symbolic links) into /Applications, as root,
# select it and run its first launch tasks. Throwaway.
set -eu
t0=$(date +%s)
rm -rf /opt/xcode /Applications/Xcode.app
tar -xf "/Volumes/My Shared Files/Xcode.tar" -C /Applications
echo "unpack seconds: $(( $(date +%s) - t0 ))"
du -sh /Applications/Xcode.app
xcode-select -s /Applications/Xcode.app/Contents/Developer
xcodebuild -license accept
xcodebuild -runFirstLaunch 2>&1 | tail -5
xcodebuild -version
echo "xcode seconds: $(( $(date +%s) - t0 ))"
