#!/bin/zsh
# X01-model-credential: a placeholder credential in Claude Code, the real
# one injected by x01-proxy. Run as the test user wbtest, in a session
# where wbtest's login keychain is unlocked. Needs x01-proxy and a claude
# binary in $DIR (see README.md). Writes logs to $DIR/out/$MODE.
#
#   MODE=oauth  (default) CLAUDE_CODE_OAUTH_TOKEN=<placeholder>; the
#               Keychain item holds a token from `claude setup-token`.
#   MODE=apikey ANTHROPIC_API_KEY=<placeholder>; the item holds an API key.
#
# The real credential stays in the proxy: claude only ever sees PLACEHOLDER.
set -u
MODE=${MODE:-oauth}
DIR=${DIR:-$HOME/x01}
OUT=$DIR/out/$MODE
# Both placeholders are 108 characters, the length of an API key. The
# proxy logs the real credential's length at start, to compare.
case $MODE in
  oauth)
    PLACEHOLDER=sk-ant-oat01-x01-placeholder-00000000000000000000000000000000000000000000000000000000000000000000000000000AA
    # Hosts that may take the token. The log says which ones got it.
    INJECT=${INJECT:-api.anthropic.com,console.anthropic.com,platform.claude.com,claude.ai}
    ;;
  apikey)
    PLACEHOLDER=sk-ant-api03-x01-placeholder-00000000000000000000000000000000000000000000000000000000000000000000000000000AA
    INJECT=${INJECT:-api.anthropic.com}
    ;;
  *) echo "MODE must be oauth or apikey, not $MODE"; exit 1 ;;
esac
mkdir -p "$OUT"
[[ $(id -un) == wbtest ]] || { echo "run as wbtest, not $(id -un)"; exit 1; }
echo "== mode $MODE, placeholder ${#PLACEHOLDER} chars, inject on $INJECT" | tee "$OUT/mode.txt"

"$DIR/x01-proxy" -listen 127.0.0.1:18080 -ca-out "$OUT/ca.pem" \
  -placeholder "$PLACEHOLDER" -key-source keychain:wraithbox-secret -inject "$INJECT" \
  >"$OUT/proxy.log" 2>&1 &
PROXY=$!
trap 'kill $PROXY' EXIT
sleep 2
kill -0 $PROXY || { cat "$OUT/proxy.log"; exit 1; }

export HTTPS_PROXY=http://127.0.0.1:18080 HTTP_PROXY=http://127.0.0.1:18080
export NODE_EXTRA_CA_CERTS=$OUT/ca.pem
unset ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_AUTH_TOKEN
if [[ $MODE == oauth ]]; then
  export CLAUDE_CODE_OAUTH_TOKEN=$PLACEHOLDER
  PROMPT_NOTE="no API key prompt expected, finish any onboarding screens"
else
  export ANTHROPIC_API_KEY=$PLACEHOLDER
  PROMPT_NOTE="answer the API key prompt"
fi
CLAUDE=$DIR/claude
cd "$DIR" || exit 1

echo "== version"; "$CLAUDE" --version | tee "$OUT/version.txt"
echo "== -p (can sit for minutes if the API errors, while Claude Code retries)"
"$CLAUDE" -p --output-format json 'Reply with the single word: pong' | tee "$OUT/p.json"
SID=$(sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p' "$OUT/p.json")
echo "== -p --resume $SID"
"$CLAUDE" -p --resume "$SID" --output-format json 'What word did you reply with?' | tee "$OUT/resume.json"
echo "== interactive: $PROMPT_NOTE, ask one question, then /exit"
"$CLAUDE"
echo "== interactive --resume $SID: ask one more question, then /exit"
"$CLAUDE" --resume "$SID"
kill $PROXY; trap - EXIT

echo "== hosts"
grep -o 'CONNECT [^ ]*' "$OUT/proxy.log" | sort | uniq -c | tee "$OUT/hosts.txt"
grep -E ' (GET|POST|PUT|PATCH|DELETE|HEAD) https' "$OUT/proxy.log" | sed 's/^.*x01: //' | sort | uniq -c >"$OUT/requests.txt"
echo "== OAuth and token paths"
grep 'TOKEN-PATH' "$OUT/proxy.log" | sed 's/^.*x01: //' | sort | uniq -c | tee "$OUT/token-paths.txt"
echo "== files holding the real credential (names only; empty is good)"
/usr/bin/security find-generic-password -s wraithbox-secret -w |
  grep -rlF -f /dev/stdin "$HOME" 2>/dev/null | grep -v '/Library/Keychains/' | tee "$OUT/key-on-disk.txt"
echo "== claude files"
ls -la "$HOME/.claude" "$HOME/.claude.json" 2>&1 | tee "$OUT/claude-files.txt"
echo "== keychain items naming claude or anthropic (service and account only)"
/usr/bin/security dump-keychain 2>/dev/null | grep -E '"(svce|acct)"' |
  grep -i -E 'claude|anthropic' | tee "$OUT/keychain-items.txt"
echo "== done; logs in $OUT"
