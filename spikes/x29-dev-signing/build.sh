#!/bin/sh
# Builds X29.app with Xcode (the profile), then adds the Go helpers and signs
# them with the personal team's Apple Development identity.
#   ./build.sh <variant>   -- a new variant changes every cdhash, as an upgrade would.
# Env: X29_ENT=full|kag    -- helper entitlements: app ID + team + group, or the group only.
set -eu
cd "$(dirname "$0")"
v=${1:-v1}
ent=${X29_ENT:-full}
team=G7BF23LV79
id="Apple Development: Leo Simons (7R9LRAX89P)"
xcodebuild -project app/X29.xcodeproj -target X29 -configuration Debug \
	SYMROOT="$PWD/work/build" OBJROOT="$PWD/work/obj" build >work/xcodebuild.log 2>&1
rm -rf "work/$v" && mkdir -p "work/$v"
app="work/$v/X29.app"
cp -R work/build/Debug/X29.app "$app"
codesign -d --entitlements - --xml "$app" >"work/$v/app.entitlements" 2>/dev/null
case $ent in
full) cp "work/$v/app.entitlements" "work/$v/helper.entitlements"
	/usr/libexec/PlistBuddy -c "Delete :com.apple.security.get-task-allow" "work/$v/helper.entitlements" ;;
kag) plutil -create xml1 "work/$v/helper.entitlements"
	plutil -insert keychain-access-groups -array "work/$v/helper.entitlements"
	plutil -insert keychain-access-groups.0 -string "$team.nl.wraithbox.store" "work/$v/helper.entitlements" ;;
esac
# X29_LAYOUT=bare: helpers as plain executables in Contents/MacOS (AMFI kills
# them, no profile). nested (default): each helper is the main executable of
# its own bundle in Contents/Helpers, with the app's profile embedded.
for h in a b; do
	(cd helper && GOWORK=off go build -o "../work/$v/x29-$h" -ldflags "-X main.variant=$v-$h" .)
	if [ "${X29_LAYOUT:-nested}" = bare ]; then
		hx="$app/Contents/MacOS/x29-$h"
		cp "work/$v/x29-$h" "$hx"
		codesign -f -s "$id" -o runtime --identifier "${X29_IDPREFIX:-nl.wraithbox.spike.x29}" \
			--entitlements "work/$v/helper.entitlements" "$hx"
		continue
	fi
	hb="$app/Contents/Helpers/x29-$h.app"
	mkdir -p "$hb/Contents/MacOS"
	cp "work/$v/x29-$h" "$hb/Contents/MacOS/x29-$h"
	cp "$app/Contents/embedded.provisionprofile" "$hb/Contents/"
	plutil -create xml1 "$hb/Contents/Info.plist"
	plutil -insert CFBundleIdentifier -string "${X29_HELPER_ID:-nl.wraithbox.spike.x29}" "$hb/Contents/Info.plist"
	plutil -insert CFBundleExecutable -string "x29-$h" "$hb/Contents/Info.plist"
	plutil -insert CFBundlePackageType -string APPL "$hb/Contents/Info.plist"
	plutil -insert LSUIElement -bool YES "$hb/Contents/Info.plist"
	codesign -f -s "$id" -o runtime --entitlements "work/$v/helper.entitlements" "$hb"
done
codesign -f -s "$id" -o runtime --entitlements "work/$v/app.entitlements" "$app"
codesign --verify --deep --strict "$app" && echo "$app verifies"
for h in a b; do
	codesign -dvvv "$app/Contents/Helpers/x29-$h.app" "$app/Contents/MacOS/x29-$h" 2>/dev/null 2>&1 | grep -E '^(Identifier|CDHash)' | sed "s/^/x29-$h: /"
done
