#!/bin/sh
# X17-image-build: read a stopped guest's unified log on the host, for
# diagnosis only. Copies the guest's diagnostics and uuidtext into a
# .logarchive and runs `log show` with a predicate.
# Usage: guestlog.sh <mounted Data volume> <archive dir> <predicate>
# Throwaway.
set -eu
data=$1 archive=$2 pred=$3
rm -rf "$archive"
mkdir -p "$archive"
cp -R "$data/private/var/db/diagnostics/." "$archive/" 2>/dev/null || true
cp -R "$data/private/var/db/uuidtext/." "$archive/" 2>/dev/null || true
[ -f "$archive/Info.plist" ] || cat >"$archive/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>OSArchiveVersion</key>
	<integer>4</integer>
</dict>
</plist>
EOF
log show "$archive" --style compact --predicate "$pred"
