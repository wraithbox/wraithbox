# X04: run every client once as the project user, through the inspecting
# relay, and record per client: exit status, the tail of its output, and
# what the relay saw (SNI, inspected or the client's alert). As root.
#   COND=<label> [ONLY="name ..."] [ONLYVAR=<variable>]
# The trust variables are those in /etc/wraithbox/env when it exists.
# Throwaway.
set -u
X=/private/tmp/x04
P=wbp-a
HP=/Users/$P
OUT=$X/matrix-$COND.jsonl
: > $OUT
BASEENV="HOME=$HP USER=$P LOGNAME=$P LANG=en_US.UTF-8 TMPDIR=/private/tmp/ PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:$HP/.local/bin"
VARS=""
[ -f /etc/wraithbox/env ] && VARS=$(sed 's/^\([A-Z_]*\)=\(.*\)$/\1="\2"/' /etc/wraithbox/env | tr '\n' ' ')
# ONLYVAR=<name>: of the trust variables, set only this one.
[ -n "${ONLYVAR:-}" ] && VARS=$(grep "^$ONLYVAR=" /etc/wraithbox/env.all | sed 's/^\([A-Z_]*\)=\(.*\)$/\1="\2"/')
U=https://example.com/
RUN=0

t() { # name extra-env command
  name=$1; extra=$2; cmd=$3
  if [ -n "${ONLY:-}" ] && ! echo " $ONLY " | grep -q " $name "; then return; fi
  RUN=$((RUN + 1))
  W=$(mktemp -d /private/tmp/x04w.XXXXXX); chown $P $W
  before=$(wc -l < $X/proxy.jsonl)
  t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
  out=$(cd $W && eval "sudo -u $P env -i $BASEENV $VARS $extra perl -e 'alarm shift; exec @ARGV' 240 /bin/sh -c \"set -o pipefail; \$cmd\"" 2>&1)
  rc=$?
  t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
  sleep 0.5
  seen=$(tail -n +$((before + 1)) $X/proxy.jsonl | /usr/bin/python3 -c '
import json, sys
rows = [json.loads(l) for l in sys.stdin if l.strip()]
print(json.dumps([[r["sni"], r["mode"], r["result"], r.get("err", "")[:90]] for r in rows]))')
  printf '%s' "$out" > $W.out
  /usr/bin/python3 - "$name" "$COND" "$rc" "$t0" "$t1" "$seen" "$extra" "$W.out" >> $OUT <<'PYEOF'
import json, sys
n, c, rc, t0, t1, seen, extra, f = sys.argv[1:]
out = open(f, errors="replace").read()
print(json.dumps({"client": n, "cond": c, "exit": int(rc), "secs": round(float(t1) - float(t0), 2), "env": extra, "relay": json.loads(seen), "out": out[-400:]}))
PYEOF
  rm -f $W.out
  rm -rf $W
  ok=$(echo "$seen" | grep -c 'client-handshake-failed')
  echo "$name rc=$rc rejected=$ok $(echo "$seen" | head -c 300)"
}

JH=/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home
B=$X/bin
C=$X/clients
t curl-apple "" "/usr/bin/curl -sS -o /dev/null -w %{http_code} $U"
t curl-apple-securetransport "CURL_SSL_BACKEND=securetransport" "/usr/bin/curl -sS -o /dev/null -w %{http_code} $U"
t curl-brew "" "/opt/homebrew/opt/curl/bin/curl -sS -o /dev/null -w %{http_code} $U"
t git-apple "" "/usr/bin/git ls-remote https://github.com/octocat/Hello-World HEAD"
t git-brew "" "/opt/homebrew/bin/git ls-remote https://github.com/octocat/Hello-World HEAD"
t gh "GH_TOKEN=x04-invalid" "gh api /zen"
t go-program "" "$B/x04-goclient $U"
t go-mod-download "GOPATH=\$PWD/gp GOFLAGS=-modcacherw" "go mod download -json golang.org/x/text@v0.20.0 | head -3"
t swift-urlsession "" "$B/x04-swiftclient $U"
t swiftpm-cli "" "cp -R $HP/pkg p && cd p && swift package resolve --cache-path \$PWD/../c --scratch-path \$PWD/../s 2>&1 | tail -3"
t xcodebuild-spm "" "cp -R $HP/pkg p && cd p && xcodebuild -resolvePackageDependencies -scheme X04 -clonedSourcePackagesDirPath \$PWD/../c -disablePackageRepositoryCache 2>&1 | tail -4"
t xcodebuild-spm-system-git "" "cp -R $HP/pkg p && cd p && xcodebuild -resolvePackageDependencies -scheme X04 -scmProvider system -clonedSourcePackagesDirPath \$PWD/../c -disablePackageRepositoryCache 2>&1 | tail -4"
t node "" "node $C/loop.js $U"
t node-use-system-ca "NODE_USE_SYSTEM_CA=1" "node $C/loop.js $U"
t npm "" "npm view left-pad version --cache \$PWD/nc"
t python-urllib "" "python3.14 $C/loop.py urllib $U"
t python-requests "" "$HP/venv/bin/python $C/loop.py requests $U"
t pip "" "$HP/venv/bin/pip download --no-deps --no-cache-dir -q -d \$PWD/d six && ls d"
t uv "UV_CACHE_DIR=\$PWD/uc" "echo six | uv pip compile - --python python3.14 -q --no-header"
t uv-native-tls "UV_CACHE_DIR=\$PWD/uc UV_NATIVE_TLS=1" "echo six | uv pip compile - --python python3.14 -q --no-header"
t ruby "" "ruby $C/loop.rb $U"
t gem "" "gem fetch rake -v 13.2.1 --clear-sources -s https://rubygems.org/"
t cargo "CARGO_HOME=\$PWD/ch" "cargo search serde --limit 1"
t java "" "$JH/bin/java $C/Loop.java $U"
t java-keychain-root "JAVA_TOOL_OPTIONS=-Djavax.net.ssl.trustStoreType=KeychainStore-ROOT" "$JH/bin/java $C/Loop.java $U"
t claude "ANTHROPIC_API_KEY=sk-ant-api03-x04-invalid" "claude -p hi --max-turns 1"
t claude-use-system-ca "ANTHROPIC_API_KEY=sk-ant-api03-x04-invalid NODE_USE_SYSTEM_CA=1" "claude -p hi --max-turns 1"
echo "ran $RUN, results in $OUT"
