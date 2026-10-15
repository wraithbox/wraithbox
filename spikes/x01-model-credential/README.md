# X01-model-credential spike: a model credential via the proxy

Throwaway code for issue #60. Not held to the project gates, never merged.

Claude Code gets a placeholder credential, and the proxy swaps in the
real one. There are two modes:

- `MODE=oauth` (the default): `CLAUDE_CODE_OAUTH_TOKEN` holds the
  placeholder, and the Keychain item `wraithbox-secret` holds a
  long-lived token from `claude setup-token`. `ANTHROPIC_API_KEY` is
  unset. This is the mode that gets run: the maintainer signs in through
  enterprise SSO and has no API key.
- `MODE=apikey`: `ANTHROPIC_API_KEY` holds the placeholder, and the
  item holds an API key. It stays unrun for want of a key.

## Files

- `proxy/`: `x01-proxy`, a TLS-inspecting HTTP CONNECT proxy.
  - It makes a CA at start and writes it to `-ca-out`.
  - It logs every CONNECT, on every host, and each request's method,
    host, path, header names and `anthropic-beta` value.
  - On the `-inject` hosts it replaces the placeholder in `x-api-key`
    or `Authorization`, as a bare value or inside `Bearer <placeholder>`.
    The log says which header and scheme it rewrote
    (`key=Authorization:Bearer:injected`), or that a placeholder went to
    a host outside `-inject` (`placeholder-not-injected`). It never logs
    a credential.
  - A request whose path holds `oauth` or `token` (an OAuth call or a
    token refresh) gets an extra `TOKEN-PATH` log line. Bodies aren't
    logged.
  - It reads the real credential with
    `security find-generic-password -s wraithbox-secret -w` from the
    Keychain of the user it runs as, and logs its length and the
    placeholder's.
  - It talks HTTP/2 upstream when the server offers it and rewrites each
    response to HTTP/1.1 for the client, chunked when the length is
    unknown, so SSE streams through.
  - `GOWORK=off go test ./...` checks injection of a bare key and a
    Bearer token against fake APIs, over HTTP/1.1 and HTTP/2 upstreams,
    with a streaming case.
- `run.sh`: the test, run as `wbtest`. It runs `-p`, `-p --resume`, an
  interactive session and an interactive `--resume`, all with the
  placeholder. Then it lists the hosts and the OAuth or token paths,
  searches `wbtest`'s home for the real credential, and lists the
  Keychain items Claude Code made. In oauth mode the proxy injects on
  `api.anthropic.com`, `console.anthropic.com`, `platform.claude.com`
  and `claude.ai` (override with `INJECT=`). In apikey mode it injects
  on `api.anthropic.com` only.

## Status

The maintainer runs it as `wbtest` (issue #214): the agent's session
can't switch to `wbtest` without a password.

- The first run, in API key mode, injected on
  every `api.anthropic.com` request. Claude Code failed with
  `Malformed_HTTP_Response`, because the proxy wrote HTTP/2 status lines
  (`HTTP/2.0 401`) to its HTTP/1.1 client. A later commit fixed that.
- The OAuth mode run on 2026-10-12 succeeded: `-p` and `-p --resume`
  answered, the interactive session answered, and the interactive
  `--resume` opened. The result is in
  `docs/spikes/X01-model-credential.md` on `main`.

The Keychain read needs the item created with `-T /usr/bin/security`.
With the default access list, `security find-generic-password -w`
exits 36 when no GUI can ask. A `sudo -u wbtest` session also needs
`security unlock-keychain` first.

## Browser login (issue #15)

`login.sh` runs the browser login part, one `STEP` at a time, as
`wbtest`, in the order `deny`, `observe`, `placeholder`, `restore`,
then `nokeychain`. Its header says what each step does. The proxy
gained what those steps need:

- `-key-source none` observes only: nothing injected.
- `-deny host[/path-prefix],...` answers 403 and logs `DENIED`.
- On a path holding `oauth` or `token` the log has the query and body
  field names, with values only for fields that aren't secret
  (`grant_type`, `client_id`, `scope`, `expires_in` and the like), and
  the same for the response (`TOKEN-PATH`, `TOKEN-RESPONSE`).
- Every request is scanned for a placeholder in any header, the query
  and the body, besides the header the proxy injects
  (`PLACEHOLDER-IN`).
- `-oauth-store keychain:wraithbox-oauth` with `-refresh-placeholder`
  injects the current real access token, and on a refresh request
  holding the refresh placeholder sends the real refresh token, keeps
  the new tokens in the item, and gives Claude Code the placeholders
  (`REFRESH #n ... refresh-rotated=... expires_in=...`).
- `x01-proxy swap` moves the login out of Claude Code's store
  (`-from`, default `keychain:Claude Code-credentials`) into
  `wraithbox-oauth`. `-show` prints its fields, `-expire-only` forces
  the next refresh, and `-restore` puts the real login back so
  `/logout` can revoke it. `x01-proxy patterns` feeds the disk search.

Spike shortcuts: `security add-generic-password -w` puts the value on
argv, briefly visible to `ps` on the test account. The `.typescript`
recordings can hold a spent login code, and stay on the Mac.

Clean up after the run: `STEP=restore`, then `claude`, `/logout`, and
`security delete-generic-password -s wraithbox-oauth`.

## Run it

As the maintainer, in a terminal logged in as `wbtest` (Fast User
Switching, or `su - wbtest` from a terminal followed by
`security unlock-keychain`, which asks for `wbtest`'s password):

```sh
# as lsimons, in a clone that has fetched the spike branch
rm -rf /Users/Shared/x01/src && mkdir -p /Users/Shared/x01/src
git archive origin/spike/x01-model-credential spikes/x01-model-credential |
  tar -x -C /Users/Shared/x01/src
cd /Users/Shared/x01/src/spikes/x01-model-credential/proxy &&
  GOWORK=off go build -o /Users/Shared/x01/x01-proxy .
# as wbtest
mkdir -p ~/x01
# rm first: a signed binary overwritten in place is killed at launch
# (exit 137, no output)
rm -f ~/x01/x01-proxy ~/x01/run.sh
rm -f ~/x01/login.sh
cp /Users/Shared/x01/x01-proxy /Users/Shared/x01/src/spikes/x01-model-credential/{run,login}.sh ~/x01/
cp "$(readlink ~lsimons/.local/bin/claude)" ~/x01/claude   # once, or install Claude Code as wbtest
~/x01/run.sh                 # oauth mode
MODE=apikey ~/x01/run.sh     # needs an API key in the item
```

After the sessions, `run.sh` fails, rather than report an empty search,
when `security find-generic-password -w` fails or returns an empty
value, because `grep -f` with no pattern matches nothing. It fails too
when `security dump-keychain` fails, and keeps that command's errors in
`keychain-errors.txt`. The 2026-10-12 run predates these checks.

`run.sh` refuses to start when 127.0.0.1:18080 is in use, which an
interrupted run can leave behind. It prints the proxy's log, or
`(empty)`, when the proxy exits at start.

When the API errors, `run.sh` can sit for minutes on one step with
no output: Claude Code retries before it gives up (the first run took
183 s to fail `-p`). Watch `out/<mode>/proxy.log` to see it is still
working.

The logs land in `~wbtest/x01/out/<mode>/`. They hold no credential:
the proxy logs header names and verdicts, and the disk search prints
file names.
