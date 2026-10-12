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
if /usr/bin/nc -z 127.0.0.1 18080 2>/dev/null; then
  echo "Port 127.0.0.1:18080 is in use, likely a proxy left by an interrupted run."
  echo "Find it with: lsof -nP -iTCP:18080 -sTCP:LISTEN, stop it, and run again."
  exit 1
fi
echo "== mode $MODE, placeholder ${#PLACEHOLDER} chars, inject on $INJECT" | tee "$OUT/mode.txt"

"$DIR/x01-proxy" -listen 127.0.0.1:18080 -ca-out "$OUT/ca.pem" \
  -placeholder "$PLACEHOLDER" -key-source keychain:wraithbox-secret -inject "$INJECT" \
  >"$OUT/proxy.log" 2>&1 &
PROXY=$!
trap 'kill $PROXY' EXIT
sleep 2
if ! kill -0 $PROXY 2>/dev/null; then
  echo "x01-proxy exited at start. Its log, $OUT/proxy.log:"
  if [[ -s $OUT/proxy.log ]]; then cat "$OUT/proxy.log"; else echo "(empty)"; fi
  echo "An empty log with exit 137 often means a binary overwritten in place: rm it, then cp it again."
  trap - EXIT
  exit 1
fi

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
# Fail rather than report "not found" when the credential can't be read:
# an empty pattern list makes grep -f match nothing.
if /usr/bin/security find-generic-password -s wraithbox-secret -w >/dev/null 2>"$OUT/key-read-errors.txt"; then
  LEN=$(/usr/bin/security find-generic-password -s wraithbox-secret -w | tr -d '\n' | wc -c)
else
  LEN=0
fi
if (( LEN == 0 )); then
  echo "FAILED: security find-generic-password -w failed or returned an empty value; the disk search did not run" |
    tee "$OUT/key-on-disk.txt"
  exit 1
fi
/usr/bin/security find-generic-password -s wraithbox-secret -w | tr -d '\n' |
  grep -rlF -f /dev/stdin "$HOME" 2>"$OUT/key-search-errors.txt" |
  grep -v '/Library/Keychains/' | tee "$OUT/key-on-disk.txt"
SEARCH=($pipestatus)
if (( SEARCH[1] != 0 )); then
  echo "FAILED: security failed during the disk search; key-on-disk.txt means nothing"
  exit 1
fi
if (( SEARCH[3] > 1 )); then
  # grep exits 2 when some files can't be read, even after a full search.
  echo "NOTE: grep could not read $(wc -l <"$OUT/key-search-errors.txt") paths; see key-search-errors.txt"
fi
echo "== claude files"
ls -la "$HOME/.claude" "$HOME/.claude.json" 2>&1 | tee "$OUT/claude-files.txt"
echo "== keychain items naming claude or anthropic (service and account only)"
/usr/bin/security dump-keychain >"$OUT/.dump" 2>"$OUT/keychain-errors.txt"
DUMP=$?
grep -E '"(svce|acct)"' "$OUT/.dump" | grep -i -E 'claude|anthropic' | tee "$OUT/keychain-items.txt"
rm -f "$OUT/.dump"
if (( DUMP != 0 )); then
  echo "FAILED: security dump-keychain exited $DUMP; see keychain-errors.txt"
  exit 1
fi
echo "== done; logs in $OUT"
