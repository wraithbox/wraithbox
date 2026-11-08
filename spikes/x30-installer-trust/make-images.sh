#!/bin/sh
# Writes the changed restore images of the cases into <dir>, from <src>.
# <dir>/extract holds the kernel caches and manifests that
# manifest.py and the flip made (see README.md).
# Usage: make-images.sh <src.ipsw> <dir> [case...]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
src=$1 dir=$2
shift 2
x=$dir/extract
k=kernelcache.release.vma2
os=141-12228-170.dmg.aea
mk() { name=$1; shift; echo "== $name"; python3 -I "$here/mkipsw.py" "$src" "$dir/$name.ipsw" "$@" >"$dir/$name.changes.jsonl"; cat "$dir/$name.changes.jsonl"; }
for c in "$@"; do
  case $c in
  c01-rezip) mk c01-rezip ;;
  c02-kernel-flip) mk c02-kernel-flip --flip "$k" ;;
  c03-os-flip) mk c03-os-flip --flip "$os" ;;
  c04-kernel-swap) mk c04-kernel-swap --put "$k=$x/kernelcache.release.mac17g" ;;
  c05-kernel-swap-manifest) mk c05-kernel-swap-manifest --put "$k=$x/kernelcache.release.mac17g" --put "BuildManifest.plist=$x/BuildManifest.c05.plist" ;;
  c06-kernel-flip-manifest) mk c06-kernel-flip-manifest --put "$k=$x/kernelcache.vma2.flipped" --put "BuildManifest.plist=$x/BuildManifest.c06.plist" ;;
  *) echo "unknown case $c"; exit 2 ;;
  esac
done
