#!/bin/zsh
# X01-model-credential, API key mode. Run as the test user wbtest, in a
# session where wbtest's login keychain is unlocked. Needs x01-proxy and
# a claude binary in $DIR (see README.md). Writes logs to $DIR/out.
# The real key stays in the proxy: claude only ever sees PLACEHOLDER.
set -u
DIR=${DIR:-$HOME/x01}
OUT=$DIR/out
PLACEHOLDER=sk-ant-api03-x01-placeholder-000000000000000000000000000000000000000000000000000000000000000000000-AAAAAAAA
mkdir -p "$OUT"
[[ $(id -un) == wbtest ]] || { echo "run as wbtest, not $(id -un)"; exit 1; }

"$DIR/x01-proxy" -listen 127.0.0.1:18080 -ca-out "$OUT/ca.pem" \
  -placeholder "$PLACEHOLDER" -key-source keychain:wraithbox-secret \
  >"$OUT/proxy.log" 2>&1 &
PROXY=$!
trap 'kill $PROXY' EXIT
sleep 2
kill -0 $PROXY || { cat "$OUT/proxy.log"; exit 1; }

export HTTPS_PROXY=http://127.0.0.1:18080 HTTP_PROXY=http://127.0.0.1:18080
export NODE_EXTRA_CA_CERTS=$OUT/ca.pem
export ANTHROPIC_API_KEY=$PLACEHOLDER
CLAUDE=$DIR/claude
cd "$DIR" || exit 1

echo "== version"; "$CLAUDE" --version | tee "$OUT/version.txt"
echo "== -p"
"$CLAUDE" -p --output-format json 'Reply with the single word: pong' | tee "$OUT/p.json"
SID=$(sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p' "$OUT/p.json")
echo "== -p --resume $SID"
"$CLAUDE" -p --resume "$SID" --output-format json 'What word did you reply with?' | tee "$OUT/resume.json"
echo "== interactive: answer the API key prompt, ask one question, then /exit"
"$CLAUDE"
echo "== interactive --resume $SID: ask one more question, then /exit"
"$CLAUDE" --resume "$SID"
kill $PROXY; trap - EXIT

echo "== hosts"
grep -o 'CONNECT [^ ]*' "$OUT/proxy.log" | sort | uniq -c | tee "$OUT/hosts.txt"
grep -E ' (GET|POST|PUT|HEAD) https' "$OUT/proxy.log" | sed 's/^.*x01: //' | sort | uniq -c >"$OUT/requests.txt"
echo "== files holding the real key (names only; empty is good)"
/usr/bin/security find-generic-password -s wraithbox-secret -w |
  grep -rlF -f /dev/stdin "$HOME" 2>/dev/null | grep -v '/Library/Keychains/' | tee "$OUT/key-on-disk.txt"
echo "== claude files"
ls -la "$HOME/.claude" "$HOME/.claude.json" 2>&1 | tee "$OUT/claude-files.txt"
echo "== keychain items naming claude or anthropic (service and account only)"
/usr/bin/security dump-keychain 2>/dev/null | grep -E '"(svce|acct)"' |
  grep -i -E 'claude|anthropic' | tee "$OUT/keychain-items.txt"
echo "== done; logs in $OUT"
