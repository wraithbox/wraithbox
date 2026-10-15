#!/bin/sh
# Builds the I61 helper as the main executable of a nested bundle, signed by
# the personal team with the binding and ca access groups, the way
# X29-dev-signing found works. Reuses X29's Xcode project for the profile.
#   ./build.sh            -> work/X29.app/Contents/Helpers/i61.app
set -eu
cd "$(dirname "$0")"
team=G7BF23LV79
id="Apple Development: Leo Simons (7R9LRAX89P)"
mkdir -p work
xcodebuild -project app/X29.xcodeproj -target X29 -configuration Debug \
	SYMROOT="$PWD/work/build" OBJROOT="$PWD/work/obj" build >work/xcodebuild.log 2>&1
rm -rf work/X29.app
cp -R work/build/Debug/X29.app work/X29.app
app=work/X29.app
codesign -d --entitlements - --xml "$app" >work/app.entitlements 2>/dev/null
plutil -create xml1 work/helper.entitlements
plutil -insert keychain-access-groups -array work/helper.entitlements
plutil -insert keychain-access-groups.0 -string "$team.nl.wraithbox.binding" work/helper.entitlements
plutil -insert keychain-access-groups.1 -string "$team.nl.wraithbox.ca" work/helper.entitlements
(cd .. && GOWORK=off CGO_ENABLED=1 go build -o i61/work/i61 -ldflags "-X main.team=$team" ./i61/helper)
hb="$app/Contents/Helpers/i61.app"
mkdir -p "$hb/Contents/MacOS"
cp work/i61 "$hb/Contents/MacOS/i61"
cp "$app/Contents/embedded.provisionprofile" "$hb/Contents/"
plutil -create xml1 "$hb/Contents/Info.plist"
plutil -insert CFBundleIdentifier -string nl.wraithbox.spike.x29 "$hb/Contents/Info.plist"
plutil -insert CFBundleExecutable -string i61 "$hb/Contents/Info.plist"
plutil -insert CFBundlePackageType -string APPL "$hb/Contents/Info.plist"
plutil -insert LSUIElement -bool YES "$hb/Contents/Info.plist"
codesign -f -s "$id" -o runtime --entitlements work/helper.entitlements "$hb"
codesign -f -s "$id" -o runtime --entitlements work/app.entitlements "$app"
codesign --verify --deep --strict "$app" && echo "$app verifies"
codesign -d --entitlements - "$hb" 2>/dev/null
