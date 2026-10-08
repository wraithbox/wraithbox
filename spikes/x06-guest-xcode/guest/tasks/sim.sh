# X06 task: simulators as the project user. List runtimes, create a device,
# boot it, install and launch the iOS app built by ios-build, shut it down.
# Throwaway.
cd ~/work || exit 1
t0=$(date +%s)
xcrun simctl list runtimes 2>&1 | head -5
rt=$(xcrun simctl list runtimes 2>/dev/null | awk '/iOS/ {print $NF; exit}')
echo "runtime: $rt"
xcrun simctl delete x06-iphone >/dev/null 2>&1
dev=$(xcrun simctl create x06-iphone "iPhone 17" "$rt" 2>&1) || dev=$(xcrun simctl create x06-iphone com.apple.CoreSimulator.SimDeviceType.iPhone-16 "$rt" 2>&1)
echo "RESULT create: $dev"
t1=$(date +%s)
xcrun simctl boot "$dev" 2>&1; b=$?
echo "RESULT boot exit=$b $(( $(date +%s) - t1 ))s"
xcrun simctl bootstatus "$dev" -b 2>&1 | tail -3 &
bs=$!
( sleep 300; kill $bs 2>/dev/null ) &
wait $bs; echo "RESULT bootstatus exit=$? $(( $(date +%s) - t1 ))s"
xcrun simctl list devices booted 2>&1 | head -4
app=~/work/dd/Build/Products/Debug-iphonesimulator/iOSApp.app
xcrun simctl install "$dev" "$app" 2>&1; echo "RESULT install exit=$?"
xcrun simctl launch "$dev" org.wraithbox.x06.iOSApp 2>&1; echo "RESULT launch exit=$?"
sleep 3
xcrun simctl spawn "$dev" launchctl list 2>/dev/null | grep -c . | sed 's/^/sim launchd jobs: /'
xcrun simctl io "$dev" screenshot ~/work/sim.png 2>&1; echo "RESULT screenshot exit=$?"
ps -axo user,pid,command | grep -E "CoreSimulator|launchd_sim|SimRender|iOSApp" | grep -v grep | cut -c1-160 | head -10
echo "RESULT total $(( $(date +%s) - t0 ))s"
