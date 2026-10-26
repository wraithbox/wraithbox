#!/bin/sh
# Builds packages/wraithbox-swift/.build/Wraith Box.app and runs its
# entitlement check: docs/spec/S17-install-and-upgrade/S17-01-bundle.md,
# "Building a bundle", steps 1 to 8, and "Signing identity". Without
# WB_TEAM_ID the bundle is ad-hoc signed. With it, Xcode signs the app
# for that team and gives it a profile, and every program is signed with
# the team's one Apple Development identity. `mise run app:bundle` runs
# it from the repository root, after version:stamp.
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
# The team that signs the build, or empty for an ad-hoc build. app:identity
# and the identity command below refuse one that isn't a team ID.
team=${WB_TEAM_ID:-}
# What codesign signs with: - (ad-hoc), or the hash of the team's Apple
# Development identity, which the build chooses after Xcode has run.
identity=-
# The one access group Xcode's signature of the app names in a team
# build. Xcode expands the prefix to the team ID and a period.
# shellcheck disable=SC2016 # Xcode expands $(AppIdentifierPrefix), not the shell
xcode_group='$(AppIdentifierPrefix)nl.wraithbox.xcode-profile'

# codesign with the hardened runtime (S17-01-bundle, "Signing identity").
# $2 is an entitlements file, relative to the repository root or
# absolute, or - for none. Only wb-vmd and, in a team build, the helpers
# have one (SEC12-least-privilege). A team signature gets no secure
# timestamp, as Xcode signs a development build, so signing needs no
# network.
sign() {
	set -- "$1" "$2" --force --sign "$identity" --options runtime
	if [ "$identity" != - ]; then
		set -- "$@" --timestamp=none
	fi
	case $2 in
	-) ;;
	/*) set -- "$@" --entitlements "$2" ;;
	*) set -- "$@" --entitlements "$root/$2" ;;
	esac
	target=$1
	shift 2
	codesign "$@" "$target"
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
	(cd "$go_dir" && "$dev" check -app "$app_dir" -disk "$mount" -stamp "$stamp_file" -bundle-id "$bundle_id" -team "$team") || status=$?
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
# Refuses a WB_TEAM_ID that isn't a team ID and an invalid WB_BUNDLE_ID.
# The app:identity task has refused them before anything was built; this
# reads the identifier.
bundle_id=$("$dev" identity)

# Step 1: the run's one version stamp, still true of the tree.
go -C "$go_dir" run ./internal/tools/buildversion check
stamp=$(cat "$stamp_file")
# In a team build the layout names each helper's entitlements file,
# its row's access groups and nothing else.
if [ -n "$team" ]; then
	"$dev" layout -team "$team" -dir "$work/entitlements" >"$work/layout"
else
	"$dev" layout >"$work/layout"
fi

# Step 2: the Go programs of the table, host and guest, for darwin/arm64.
(cd "$go_dir" && "$dev" go-build -stamp "$stamp_file" -out "$work/go")

# Step 3: the Swift package's programs of the table (wb-vmd), linked
# with the stamp as swift:build links them. SwiftPM's native build
# system doesn't track what is in the stamp file, so with no Swift change
# it keeps a program linked with an older stamp. Removing the program
# first makes SwiftPM link it again with this run's stamp. swift build
# gets /dev/null as stdin, so it reads none of the layout's lines.
swift_bin=$(swift build -c release --package-path "$swift_dir" --force-resolved-versions --show-bin-path)
while read -r source _ name _; do
	[ "$source" = swiftpm ] || continue
	rm -f "$swift_bin/$name"
	swift build -c release --package-path "$swift_dir" --force-resolved-versions --product "$name" \
		-Xlinker -sectcreate -Xlinker __TEXT -Xlinker __wb_version -Xlinker "$stamp_file" </dev/null
done <"$work/layout"

# Step 4: the app, whose executable is the notification helper. Its
# deployment target is Package.swift's, so one edit moves both. Without
# a team Xcode signs nothing (CODE_SIGNING_ALLOWED=NO). With one it signs
# automatically for that team, and -allowProvisioningUpdates lets it
# register the bundle identifier and fetch its profile, which a new
# identifier needs (S12-03-host-prerequisites, "Development builds on
# macOS"). Step 7 signs the app again. The stamp goes into the same
# section as in the Swift package's programs. Its build directories are
# in the work directory, which every build removes first, so Xcode links
# the helper with this run's stamp every time. xcodebuild takes
# -derivedDataPath only with -scheme, so Xcode may write to
# ~/Library/Developer/Xcode/DerivedData (docs/agents/host-work.md).
manifest=$(swift package dump-package --package-path "$swift_dir")
platform=$(printf '%s' "$manifest" | plutil -extract platforms.0.platformName raw -o - -)
target=$(printf '%s' "$manifest" | plutil -extract platforms.0.version raw -o - -)
if [ "$platform" != macos ]; then
	echo "app:bundle: Package.swift's first platform is $platform, not macos" >&2
	exit 1
fi
# Xcode makes a profile only for a target whose entitlements need one,
# and the app target has none of its own. So a team build hands it one
# keychain-access-groups entry, as X29-dev-signing signed its app with
# one. The group is in no row of S15-02-secret-store, and step 7 signs
# the app again without entitlements, so the app never holds it. If that
# signing were dropped, the entitlement check would refuse the app's
# group (keychain-other, SEC04-no-guest-secrets).
if [ -n "$team" ]; then
	xcode_entitlements=$work/xcode-profile.entitlements
	plutil -create xml1 "$xcode_entitlements"
	plutil -insert keychain-access-groups -array "$xcode_entitlements"
	plutil -insert keychain-access-groups -string "$xcode_group" -append "$xcode_entitlements"
	set -- -allowProvisioningUpdates CODE_SIGN_STYLE=Automatic DEVELOPMENT_TEAM="$team" CODE_SIGN_IDENTITY="Apple Development" \
		CODE_SIGN_ENTITLEMENTS="$xcode_entitlements"
else
	set -- CODE_SIGNING_ALLOWED=NO
fi
xcodebuild -quiet -project "$swift_dir/WraithBox.xcodeproj" -target WraithBox -configuration Release \
	SYMROOT="$work/xcode/sym" OBJROOT="$work/xcode/obj" \
	"$@" \
	MACOSX_DEPLOYMENT_TARGET="$target" \
	PRODUCT_BUNDLE_IDENTIFIER="$bundle_id" \
	"OTHER_LDFLAGS=\"-Wl,-sectcreate,__TEXT,__wb_version,$stamp_file\"" \
	build
app="$work/Wraith Box.app"
cp -R "$work/xcode/sym/Release/Wraith Box.app" "$app"

# A team build stops here unless Xcode gave the app a profile of the
# team (S12-03-host-prerequisites, "Missing profile"), and then chooses
# the team's one Apple Development identity by the OU of its
# certificate (S17-01-bundle, "Signing identity").
if [ -n "$team" ]; then
	"$dev" profile -app "$app" -team "$team"
	identity=$("$dev" sign-identity -team "$team")
fi

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
# helper bundle, then the app. An ad-hoc helper bundle has no profile. In
# a team build each helper bundle gets a copy of the app's profile, which
# macOS matches to the helper by its bundle identifier, the app's
# (X29-dev-signing).
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
		if [ -n "$team" ]; then
			cp "$app/Contents/embedded.provisionprofile" "$helper/Contents/embedded.provisionprofile"
		fi
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
