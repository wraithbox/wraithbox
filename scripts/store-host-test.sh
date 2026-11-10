#!/bin/sh
# Runs the host tests of the secret store (C05-credentials-and-store,
# "Host tests"): Go test binaries signed with the team's Apple
# Development identity into bundles of their own, which reach the data
# protection keychain as the app's helpers do. `mise run store:host-test`
# runs it from the repository root. It is needs-host and attended
# (docs/agents/host-work.md): agents write it, the maintainer runs it.
#
#   WB_TEAM_ID=<team> sh scripts/store-host-test.sh          # every step but the lock
#   WB_TEAM_ID=<team> sh scripts/store-host-test.sh --lock   # the lock step alone
#
# It needs a team build of the app first (WB_TEAM_ID=<team> mise run
# app:bundle): each test bundle gets a copy of that app's profile, which
# macOS matches by the bundle identifier, the app's, as for a helper
# (S10-02-packaging-and-signing, "Helper bundles"; X29-dev-signing).
#
# Each test bundle is .scratch/store-test/<bundle>.app, with the test
# binary as its executable <bundle> and keychain-access-groups with the
# groups of its roles and nothing else. Each run starts the binary with
# an empty environment but for WB_STORE_HOST_TEST=1 and
# WB_STORE_HOST_STEP (decision 8 on I505). A test package for another
# backend call is one more line in each table below.
set -eu

if [ "$(uname -s)" != Darwin ]; then
	echo "store:host-test: the Keychain tests need macOS; skipped on $(uname -s)"
	exit 0
fi

lock=no
case ${1:-} in
'') ;;
--lock) lock=yes ;;
*)
	echo "usage: sh scripts/store-host-test.sh [--lock]" >&2
	exit 2
	;;
esac

team=${WB_TEAM_ID:-}
if [ -z "$team" ]; then
	echo "store:host-test: set WB_TEAM_ID to the team of the app's profile; the tests need a team signature (S15-02-secret-store)" >&2
	exit 2
fi

root=$(pwd -P)
go_dir=$root/packages/wraithbox-go
app="$root/packages/wraithbox-swift/.build/Wraith Box.app"
work=$root/.scratch/store-test
dev=$work/wb-dev-app

rm -rf "$work"
mkdir -p "$work"
go -C "$go_dir" build -o "$dev" ./cmd/wb-dev-app
# Refuses a WB_TEAM_ID that isn't a team ID.
bundle_id=$("$dev" identity)
# Refuses an app without a profile of the team.
"$dev" profile -app "$app" -team "$team"
identity=$("$dev" sign-identity -team "$team")

# build <bundle> <package> <roles>: the test binary of <package>, relative
# to packages/wraithbox-go, in .scratch/store-test/<bundle>.app, signed
# with the groups of <roles>, a comma-separated list, as the build signs a
# helper (scripts/app-bundle.sh).
build() {
	bundle=$work/$1.app
	mkdir -p "$bundle/Contents/MacOS"
	(cd "$go_dir" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go test -c -o "$bundle/Contents/MacOS/$1" "$2")
	plist=$bundle/Contents/Info.plist
	plutil -create xml1 "$plist"
	plutil -insert CFBundleIdentifier -string "$bundle_id" "$plist"
	plutil -insert CFBundleExecutable -string "$1" "$plist"
	plutil -insert CFBundlePackageType -string APPL "$plist"
	plutil -insert LSUIElement -bool YES "$plist"
	cp "$app/Contents/embedded.provisionprofile" "$bundle/Contents/embedded.provisionprofile"
	entitlements=$work/$1.entitlements
	"$dev" entitlements -team "$team" -roles "$3" >"$entitlements"
	codesign --force --sign "$identity" --options runtime --timestamp=none --entitlements "$entitlements" "$bundle"
	codesign --verify --strict "$bundle"
	codesign -d --entitlements - "$bundle" 2>&1
}

# run <bundle> <step> <tests>: the bundle's binary with the step, running
# the tests that match the regular expression <tests>. A failed run fails
# the task once every run is done.
failed=
run() {
	echo "store:host-test: $1, step '${2}'"
	if ! env -i WB_STORE_HOST_TEST=1 WB_STORE_HOST_STEP="$2" "$work/$1.app/Contents/MacOS/$1" \
		-test.v -test.count=1 -test.timeout=15m -test.run "$3"; then
		failed="$failed $1/${2:-main}"
	fi
}

build wb-store-test ./internal/platform/secretstore binding,ca
build wb-store-test-binding ./internal/platform/secretstore binding

if [ "$lock" = yes ]; then
	run wb-store-test lock '^TestC05HostLocked$'
else
	run wb-store-test '' '^TestC05Host'
	run wb-store-test-binding binding-only '^TestC05HostGroupMismatch$'
	run wb-store-test confine-after '^TestC05HostConfineAfterOpen$'
	run wb-store-test confine-first '^TestC05HostConfineFirst$'
fi

if [ -n "$failed" ]; then
	echo "store:host-test: FAIL:$failed" >&2
	exit 1
fi
echo "store:host-test: PASS"
