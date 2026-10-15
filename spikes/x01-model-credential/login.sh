#!/bin/zsh
# X01-model-credential, browser login (issue #15). Run as wbtest, one step
# at a time, in this order:
#
#   STEP=deny         logged out. The proxy refuses the login hosts,
#                     /v1/oauth/hello included. Type /login: is a URL shown?
#   STEP=observe      logged out. Nothing injected or refused. /login with
#                     SSO, ask one question, /exit. Shows where the login landed.
#   STEP=placeholder  after observe. swap moves the real login into the
#                     Keychain item wraithbox-oauth, leaves placeholders with
#                     a past expiry, and the proxy answers the refresh.
#   STEP=restore      puts the real login back, for /logout to revoke it.
#   STEP=nokeychain   from `su - wbtest` WITHOUT unlocking the Keychain,
#                     logged out: /login, then where the login landed.
#
# Needs x01-proxy and claude in $DIR (README.md). Logs in $DIR/out/login-$STEP.
# Interactive sessions are recorded with script(1) in <name>.typescript.
set -u
STEP=${STEP:?set STEP to deny, observe, placeholder, restore or nokeychain}
DIR=${DIR:-$HOME/x01}
OUT=$DIR/out/login-$STEP
FROM=${FROM:-keychain:Claude Code-credentials}
STORE=wraithbox-oauth
ACCESS_PH=sk-ant-oat01-x01-placeholder-00000000000000000000000000000000000000000000000000000000000000000000000000000AA
REFRESH_PH=sk-ant-ort01-x01-refresh-placeholder-000000000000000000000000000000000000000000000000000000000000000000000AA
LOGIN_HOSTS=platform.claude.com,claude.ai,console.anthropic.com
PROXY_BIN=$DIR/x01-proxy
CLAUDE=$DIR/claude

[[ $(id -un) == wbtest ]] || { echo "run as wbtest, not $(id -un)"; exit 1; }
mkdir -p "$OUT"
cd "$DIR" || exit 1

# The trap is set here, not in start_proxy: in zsh an EXIT trap set in a
# function runs when the function returns, which killed the proxy before
# Claude Code started (the first deny and observe runs, 2026-10-15).
PROXY=
trap '[[ -n $PROXY ]] && kill $PROXY 2>/dev/null' EXIT

start_proxy() {
  if /usr/bin/nc -z 127.0.0.1 18080 2>/dev/null; then
    echo "Port 127.0.0.1:18080 is in use: lsof -nP -iTCP:18080 -sTCP:LISTEN, stop it, run again."
    exit 1
  fi
  echo "== proxy $*" | tee "$OUT/mode.txt"
  "$PROXY_BIN" -listen 127.0.0.1:18080 -ca-out "$OUT/ca.pem" "$@" >"$OUT/proxy.log" 2>&1 &
  PROXY=$!
  sleep 2
  if ! kill -0 $PROXY 2>/dev/null; then
    echo "x01-proxy exited at start:"; cat "$OUT/proxy.log"; PROXY=; exit 1
  fi
  export HTTPS_PROXY=http://127.0.0.1:18080 HTTP_PROXY=http://127.0.0.1:18080
  export NODE_EXTRA_CA_CERTS=$OUT/ca.pem
}

# Fails the step when the proxy died during the session: then Claude Code
# talked to a closed port and the logs mean nothing.
stop_proxy() {
  if ! kill -0 $PROXY 2>/dev/null; then
    echo "FAILED: x01-proxy was not running at the end of the session. Its log:"
    cat "$OUT/proxy.log"; PROXY=; exit 1
  fi
  kill $PROXY; PROXY=
}

# Claude Code reads these before its stored login: none may be set.
unset ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_AUTH_TOKEN

where_login_landed() {
  echo "== claude files"
  ls -la "$HOME/.claude" "$HOME/.claude.json" "$HOME/.claude/.credentials.json" 2>&1 | tee "$OUT/claude-files.txt"
  echo "== keychain items naming claude or anthropic (service and account only)"
  /usr/bin/security dump-keychain >"$OUT/.dump" 2>"$OUT/keychain-errors.txt"
  echo "dump-keychain exit $?" >>"$OUT/keychain-errors.txt"
  grep -E '"(svce|acct)"' "$OUT/.dump" | grep -i -E 'claude|anthropic' | tee "$OUT/keychain-items.txt"
  rm -f "$OUT/.dump"
  echo "keychain errors: $(cat "$OUT/keychain-errors.txt")"
  echo "== default keychain and its state"
  { /usr/bin/security default-keychain; /usr/bin/security show-keychain-info 2>&1; } | tee "$OUT/keychain-state.txt"
}

summary() {
  echo "== hosts"
  grep -o 'CONNECT [^ ]*' "$OUT/proxy.log" | sort | uniq -c | tee "$OUT/hosts.txt"
  grep -E ' -> HTTP' "$OUT/proxy.log" | sed 's/^.*x01: //' | sort | uniq -c >"$OUT/requests.txt"
  echo "== denied, token paths, refreshes, placeholder leaks"
  grep -E 'DENIED|TOKEN-|REFRESH|PLACEHOLDER-IN|REAL-TOKEN' "$OUT/proxy.log" | sed 's/^.*x01: //' |
    tee "$OUT/oauth.txt"
}

# observe and placeholder need an unlocked login Keychain (in `su - wbtest`
# run `security unlock-keychain` first). nokeychain needs it locked.
if /usr/bin/security show-keychain-info >/dev/null 2>&1; then UNLOCKED=1; else UNLOCKED=0; fi
case $STEP:$UNLOCKED in
observe:0|placeholder:0|restore:0)
  echo "The login Keychain is locked: run security unlock-keychain, then this step again."; exit 1 ;;
nokeychain:1)
  echo "The login Keychain is unlocked: run security lock-keychain, then this step again."; exit 1 ;;
esac

case $STEP in
deny)
  start_proxy -key-source none -deny "$LOGIN_HOSTS"
  echo "== claude, logged out. Type /login and pick the subscription."
  echo "   Note whether a URL is shown, and what the screen says. Do NOT finish"
  echo "   the login. Then Ctrl-C twice, or /exit."
  script -q "$OUT/claude.typescript" "$CLAUDE"
  stop_proxy; summary
  echo "== URLs in the session (the authorize URL holds a PKCE challenge, not a secret)"
  grep -aoE 'https://[^ [:cntrl:]]*' "$OUT/claude.typescript" | sort -u | tee "$OUT/urls.txt"
  ;;
observe)
  start_proxy -key-source none
  echo "== claude, logged out: /login with SSO in the browser, ask one question, /exit"
  script -q "$OUT/claude.typescript" "$CLAUDE"
  stop_proxy; summary; where_login_landed
  "$PROXY_BIN" swap -from "$FROM" -show | tee "$OUT/login-fields.txt"
  ;;
placeholder)
  echo "== swap: the real login into $STORE, placeholders and a past expiry into $FROM"
  echo "   (the Keychain may ask to let security read the item: allow it)"
  "$PROXY_BIN" swap -from "$FROM" -store "$STORE" -access-placeholder "$ACCESS_PH" \
    -refresh-placeholder "$REFRESH_PH" -expire | tee "$OUT/swap.txt" || exit 1
  start_proxy -oauth-store "keychain:$STORE" -placeholder "$ACCESS_PH" \
    -refresh-placeholder "$REFRESH_PH" -inject "api.anthropic.com,$LOGIN_HOSTS"
  echo "== -p, expired: Claude Code should refresh first"
  "$CLAUDE" -p --output-format json 'Reply with the single word: pong' | tee "$OUT/p.json"
  SID=$(sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p' "$OUT/p.json")
  "$PROXY_BIN" swap -from "$FROM" -show | tee "$OUT/after-refresh-1.txt"
  echo "== expire again: the second refresh uses the refresh token the proxy got back"
  "$PROXY_BIN" swap -from "$FROM" -expire-only | tee "$OUT/expire-2.txt"
  echo "== -p --resume $SID"
  "$CLAUDE" -p --resume "$SID" --output-format json 'What word did you reply with?' | tee "$OUT/resume.json"
  "$PROXY_BIN" swap -from "$FROM" -show | tee "$OUT/after-refresh-2.txt"
  echo "== interactive: ask one question, then /exit"
  script -q "$OUT/claude.typescript" "$CLAUDE"
  stop_proxy; summary; where_login_landed
  echo "== files holding a real token (names only; empty is good)"
  "$PROXY_BIN" patterns -store "$STORE" | grep -rlF -f /dev/stdin "$HOME" 2>"$OUT/search-errors.txt" |
    grep -v -e '/Library/Keychains/' | tee "$OUT/token-on-disk.txt"
  SEARCH=($pipestatus)
  if (( SEARCH[1] != 0 )); then
    echo "FAILED: patterns failed; the search means nothing" | tee "$OUT/token-on-disk.txt"; exit 1
  fi
  ;;
restore)
  "$PROXY_BIN" swap -from "$FROM" -store "$STORE" -restore | tee "$OUT/restore.txt" || exit 1
  # Observed, not injected: the refresh with the restored token, and the
  # revocation /logout makes, both show in oauth.txt.
  start_proxy -key-source none
  echo "== claude with the restored login (expired, so it refreshes first). Ask one"
  echo "   question if it lets you, then /logout, then /exit"
  script -q "$OUT/claude.typescript" "$CLAUDE"
  stop_proxy; summary; where_login_landed
  /usr/bin/security delete-generic-password -s "$STORE" -a x01 >/dev/null && echo "== deleted $STORE"
  ;;
nokeychain)
  /usr/bin/security show-keychain-info 2>&1 | tee "$OUT/keychain-before.txt"
  start_proxy -key-source none
  echo "== claude, logged out, Keychain locked: /login (paste the code if no browser opens),"
  echo "   ask one question, /exit"
  script -q "$OUT/claude.typescript" "$CLAUDE"
  stop_proxy; summary; where_login_landed
  ;;
*) echo "unknown STEP $STEP"; exit 1 ;;
esac
echo "== done; logs in $OUT"
