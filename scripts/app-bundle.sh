#!/bin/sh
# Builds packages/wraithbox-swift/.build/Wraith Box.app, ad-hoc signed,
# and runs its entitlement check: docs/spec/S17-install-and-upgrade/
# S17-01-bundle.md, "Building a bundle", steps 1 to 8, with the ad-hoc
# half of "Signing identity". `mise run app:bundle` runs it from the
# repository root, after version:stamp.
#
#   sh scripts/app-bundle.sh build        # build, check, and move the bundle into place
#   sh scripts/app-bundle.sh check [APP]  # check a built bundle again
#
# The programs come from one table, the layout in
# packages/wraithbox-go/internal/appbundle, which `wb-dev-app layout`
# prints. The Go half of the build (the bundle identifier, the Go builds
# and the check) is packages/wraithbox-go/cmd/wb-dev-app.
#
# The bundle is put together in .build/app-work and moved into place
# only when the check passes, so a bundle that fails it is never at the
# path app:install reads.
set -eu

if [ "$(uname -s)" != Darwin ]; then
	echo "app:bundle: the bundle needs macOS; skipped on $(uname -s)"
	exit 0
fi

root=$(pwd -P)
go_dir=$root/packages/wraithbox-go
swift_dir=$root/packages/wraithbox-swift
out="$swift_dir/.build/Wraith Box.app"
work=$swift_dir/.build/app-work
stamp_file=$root/build/version
dev=$work/wb-dev-app
tools_disk=Contents/Resources/wb-guest-tools.img

# codesign with the hardened runtime, ad-hoc (S17-01-bundle, "Signing
# identity"). $2 is an entitlements file relative to the repository root,
# or - for none. Only wb-vmd has one (SEC12-least-privilege).
sign() {
	if [ "$2" = - ]; then
		codesign --force --sign - --options runtime "$1"
	else
		codesign --force --sign - --options runtime --entitlements "$root/$2" "$1"
	fi
}

# Detaches the tools disk the check attached at $mount: a plain detach,
# and a forced one only when the plain one fails. Called only once the
# attach has succeeded, so $mount is the disk's mount point.
detach_disk() {
	hdiutil detach -quiet "$mount" || hdiutil detach -quiet -force "$mount"
}

# The entitlement check of step 8, on the bundle $1, with its tools disk
# attached read-only for the time of the check. A relative $1 is taken
# from the repository root, where mise runs the task. Once the attach has
# succeeded, traps detach the disk when the script exits or is
# interrupted during the check, so no mount is left in the work
# directory. A failed attach detaches nothing.
check() {
	case $1 in
	/*) app_dir=$1 ;;
	*) app_dir=$root/$1 ;;
	esac
	mount=$work/tools-disk
	mkdir -p "$mount"
	hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$mount" "$app_dir/$tools_disk" >/dev/null
	trap 'detach_disk' EXIT
	trap 'detach_disk; exit 130' INT TERM
	status=0
	(cd "$go_dir" && "$dev" check -app "$app_dir" -disk "$mount" -stamp "$stamp_file" -bundle-id "$bundle_id") || status=$?
	trap - EXIT INT TERM
	detach_disk
	return "$status"
}

build_dev() {
	mkdir -p "$work"
	go -C "$go_dir" build -o "$dev" ./cmd/wb-dev-app
}

if [ "${1:-}" = check ]; then
	build_dev
	bundle_id=$("$dev" identity)
	check "${2:-$out}"
	exit 0
fi
if [ "${1:-}" != build ]; then
	echo "usage: sh scripts/app-bundle.sh build | check [APP]" >&2
	exit 2
fi

rm -rf "$work"
build_dev
# Refuses WB_TEAM_ID and an invalid WB_BUNDLE_ID. The app:identity task
# has refused them before anything was built; this reads the identifier.
bundle_id=$("$dev" identity)

# Step 1: the run's one version stamp, still true of the tree.
go -C "$go_dir" run ./internal/tools/buildversion check
stamp=$(cat "$stamp_file")
"$dev" layout >"$work/layout"

# Step 2: the Go programs of the table, host and guest, for darwin/arm64.
(cd "$go_dir" && "$dev" go-build -stamp "$stamp_file" -out "$work/go")

# Step 3: wb-vmd, linked with the stamp as swift:build links it.
swift build -c release --package-path "$swift_dir" --force-resolved-versions --product wb-vmd \
	-Xlinker -sectcreate -Xlinker __TEXT -Xlinker __wb_version -Xlinker "$stamp_file"
swift_bin=$(swift build -c release --package-path "$swift_dir" --force-resolved-versions --show-bin-path)

# Step 4: the app, whose executable is the notification helper. Its
# deployment target is Package.swift's, so one edit moves both. Xcode
# signs nothing (CODE_SIGNING_ALLOWED=NO). The stamp goes into the same
# section as in the Swift package's programs. xcodebuild takes
# -derivedDataPath only with -scheme, so Xcode may write to
# ~/Library/Developer/Xcode/DerivedData (docs/agents/host-work.md).
manifest=$(swift package dump-package --package-path "$swift_dir")
platform=$(printf '%s' "$manifest" | plutil -extract platforms.0.platformName raw -o - -)
target=$(printf '%s' "$manifest" | plutil -extract platforms.0.version raw -o - -)
if [ "$platform" != macos ]; then
	echo "app:bundle: Package.swift's first platform is $platform, not macos" >&2
	exit 1
fi
xcodebuild -quiet -project "$swift_dir/WraithBox.xcodeproj" -target WraithBox -configuration Release \
	SYMROOT="$work/xcode/sym" OBJROOT="$work/xcode/obj" \
	CODE_SIGNING_ALLOWED=NO \
	MACOSX_DEPLOYMENT_TARGET="$target" \
	PRODUCT_BUNDLE_IDENTIFIER="$bundle_id" \
	"OTHER_LDFLAGS=\"-Wl,-sectcreate,__TEXT,__wb_version,$stamp_file\"" \
	build
app="$work/Wraith Box.app"
cp -R "$work/xcode/sym/Release/Wraith Box.app" "$app"

# The file each program of the table was built to.
built() {
	case $1 in
	go) echo "$work/go/$2" ;;
	swiftpm) echo "$swift_bin/$2" ;;
	*) echo "app:bundle: no built file for source $1" >&2 && return 1 ;;
	esac
}

# Step 5: the guest tools disk. Its programs are signed as in step 7, and
# its VERSION file holds the stamp. One APFS volume in a raw image (UDTO),
# the format Virtualization attaches. hdiutil needs no administrator
# rights.
mkdir -p "$work/disk"
while read -r source place name entitlements; do
	[ "$place" = disk ] || continue
	cp "$(built "$source" "$name")" "$work/disk/$name"
	sign "$work/disk/$name" "$entitlements"
done <"$work/layout"
printf '%s' "$stamp" >"$work/disk/VERSION"
hdiutil create -quiet -srcfolder "$work/disk" -fs APFS -volname wb-guest-tools -format UDTO -o "$work/tools"
mkdir -p "$app/Contents/Resources"
mv "$work/tools.cdr" "$app/$tools_disk"

# Steps 6 and 7: the programs go into the app, each with a group into a
# helper bundle of its own (S10-02-packaging-and-signing, "Helper
# bundles"), and are signed from the inside out: each program, then each
# helper bundle, then the app. An ad-hoc helper bundle has no profile.
while read -r source place name entitlements; do
	case $place in
	macos)
		cp "$(built "$source" "$name")" "$app/Contents/MacOS/$name"
		sign "$app/Contents/MacOS/$name" "$entitlements"
		;;
	helper)
		helper="$app/Contents/Helpers/$name.app"
		mkdir -p "$helper/Contents/MacOS"
		cp "$(built "$source" "$name")" "$helper/Contents/MacOS/$name"
		plist=$helper/Contents/Info.plist
		plutil -create xml1 "$plist"
		plutil -insert CFBundleIdentifier -string "$bundle_id" "$plist"
		plutil -insert CFBundleExecutable -string "$name" "$plist"
		plutil -insert CFBundlePackageType -string APPL "$plist"
		plutil -insert LSUIElement -bool YES "$plist"
		sign "$helper" "$entitlements"
		;;
	esac
done <"$work/layout"

# The build version wb-hostd and wb setup read, in an XML Info.plist they
# read with a plain XML reader.
plutil -replace WBBuildVersion -string "$stamp" "$app/Contents/Info.plist"
plutil -convert xml1 "$app/Contents/Info.plist"
sign "$app" -

# Step 8: the entitlement check and codesign --verify. Either failing
# fails the build, and leaves the bundle in the work directory.
check "$app"
rm -rf "$out"
mv "$app" "$out"
echo "app:bundle: built $out ($stamp, $bundle_id)"
