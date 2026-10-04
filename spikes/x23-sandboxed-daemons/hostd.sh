#!/bin/bash
# Usage: hostd.sh <mech> <hostd profile> [git] [git root] [hostd binary] [extra hostd flag]
# Runs hostd with fresh state directories under .scratch/x23. The profile
# gets generated metadata-read rules for the ancestors of <data>, which
# git needs to resolve real paths.
cd "$(dirname "$0")" || exit 1
mech=$1 prof=$2 git=${3:-/usr/bin/git} gitroot=${4:-/Library/Developer/CommandLineTools} bin=${5:-hostd-nocgo} extra=${6:-}
root=$(cd ../.. && pwd)/.scratch/x23
rm -rf "$root"
mkdir -p "$root/config" "$root/data/run" "$root/logs"
echo 'x = 1' >"$root/config/config.toml"
sp=$PWD
gen=$root/hostd.sb
cp "$prof" "$gen"
d=$root/data
while [ "$d" != / ]; do
  d=$(dirname "$d")
  echo "(allow file-read-metadata (literal \"$d\"))" >>"$gen"
done
# Test only: the local-path push and clone start git-receive-pack and
# git-upload-pack through /bin/sh. The real gateway runs them directly.
echo "(allow process-exec file-read* (literal \"/bin/sh\") (literal \"/bin/bash\") (literal \"/private/var/select/sh\"))" >>"$gen"
cd "$root" || exit 1
# shellcheck disable=SC2086
"$sp/bin/$bin" -mech "$mech" -profile "$gen" $extra \
  -D "CONFIG=$root/config" -D "DATA=$root/data" -D "LOGS=$root/logs" -D "GIT=$git" -D "GITROOT=$gitroot" -D "NETD=$sp/bin/netd-nocgo" \
  -config "$root/config" -data "$root/data" -logs "$root/logs" -sock data/run/hostd.sock -git "$git" \
  -netd "$sp/bin/netd-nocgo" -netd-profile "$sp/profiles/netd.sb" \
  -readfile "$sp/go.mod" -writedir "$sp" 2>&1 &
pid=$!
sleep 5
echo hello | nc -U data/run/hostd.sock
wait $pid
echo "exit=$?"
