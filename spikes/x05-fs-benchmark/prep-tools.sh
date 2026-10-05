#!/bin/sh
# X05-fs-benchmark spike: copy the toolchains the guest needs into
# <work>/tools, so the guest and the host run the same binaries:
# node 24 and npm (from mise), Apple's git from Xcode (system libraries
# only), and Go (from mise). A stock guest has none of them.
# Usage: prep-tools.sh <work dir>
set -eu
W=$1
NODE=$HOME/.local/share/mise/installs/node/24
GO=$HOME/.local/share/mise/installs/go/1.27.1
XC=/Applications/Xcode.app/Contents/Developer/usr
mkdir -p "$W/tools/node/bin" "$W/tools/node/lib/node_modules" "$W/tools/vcs/bin"
cp "$NODE/bin/node" "$W/tools/node/bin/"
cp -R "$NODE/lib/node_modules/npm" "$W/tools/node/lib/node_modules/"
ln -sf ../lib/node_modules/npm/bin/npm-cli.js "$W/tools/node/bin/npm"
cp "$XC/bin/git" "$W/tools/vcs/bin/"
cp -R "$XC/libexec/git-core" "$W/tools/vcs/"
rm -rf "$W/tools/go"
cp -R "$GO" "$W/tools/go"
du -sh "$W"/tools/*
