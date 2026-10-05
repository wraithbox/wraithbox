#!/bin/sh
# X05-fs-benchmark spike: the npm ci workload. The docs site's package.json
# (Astro, Starlight, sharp, TypeScript, cspell, playwright), locked with
# npm, and an npm cache that holds every package, so `npm ci --offline`
# needs no network. The guest gets a copy of the cache: it has no route
# to a registry.
# Usage: prep-npm.sh <work dir> <package.json>
set -eu
W=$1
mkdir -p "$W/npm/proj"
cp "$2" "$W/npm/proj/package.json"
export PATH="$W/tools/node/bin:$PATH" npm_config_cache="$W/npm/cache"
cd "$W/npm/proj"
npm install --package-lock-only --no-audit --no-fund
npm ci --no-audit --no-fund
rm -rf node_modules
# the offline run the benchmark repeats, to prove the cache is complete
npm ci --offline --no-audit --no-fund
du -sh node_modules "$W/npm/cache"
find node_modules -type f | wc -l
