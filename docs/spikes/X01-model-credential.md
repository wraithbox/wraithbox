# X01 - Model credential via the proxy

**Purpose:** The result of the spike for I60, the part of I15 that
doesn't need a browser login, and for the browser login in I15
("Browser login (I15)"). Does Claude Code work with a placeholder
credential while a host-side proxy injects the real one, and which
hosts does it contact, for the model API profile of
S07-egress-gateway?

**Requirements:** SEC04-no-guest-secrets, SEC05-default-deny,
SEC10-audit, FR01-drop-in, FR13-claude-state

**Brief:** B60-model-credential, and B15-browser-login for I15

## For review

### The browser login (I15)

- **Decides:** whether V1 supports a browser login as a model
  credential, and whether the terminal filter hides the authorize URL
  Claude Code prints in a guest.
- **You are approving:** the answer "yes, with conditions" for a
  browser login. With placeholder access and refresh tokens in Claude
  Code's store, and the proxy holding the real pair and answering the
  refresh, Claude Code answered in `-p`, `-p --resume` and an
  interactive session, across two forced refreshes. The conditions:
  only the host refreshes, because each refresh rotates the refresh
  token, the login comes from outside the guest, and the user
  signs in again about every 30 days. And the answer "no" to the
  question from S14-claude-in-guest: refusing `/v1/oauth/hello` doesn't
  stop `/login` from printing the authorize URL.
- **Controls touched:** none weakened. SEC04-no-guest-secrets holds for
  the measured mode: Claude Code had only placeholders. It wouldn't
  hold for a login made in the guest, which the guest's Claude Code
  stores in its Keychain, or in plaintext when there is none.
- **Assumed or inferred:**
  - That a Pro or Max login behaves like the enterprise SSO login
    measured here, with the same scopes, lifetimes, and refresh.
  - That a rotated-away refresh token still works after more than
    the five minutes measured. A short grace period looks the same.
  - That `wb` may run the login with Claude Code's public client ID.
    OpenShell holds back from this until Anthropic approves an OAuth
    client identity for third-party tools (X00-index).
  - That the long-lived token didn't reach the organization's plugin
    and skill paths because of its scope. Inferred: it reached none of
    them on 2026-10-12, and the scope wasn't logged then.
- **Decisions:** made on I15 on 2026-10-15, as B15-browser-login
  recommended.
  1. The browser login isn't in V1. The long-lived token stays the
     subscription mode, and I257 keeps the measured design for later.
  2. The authorize URL gets no terminal filter. The warning in
     S14-claude-in-guest stays.
- **Brief:** B15-browser-login

### The long-lived token (I60, decided 2026-10-12)

- **Decides:** the answer for a long-lived subscription token, the
  host list for the model API profile, three questions about what
  Claude Code may reach besides the model, and whether the long-lived
  token becomes a supported mode. The spec changes wait for those
  decisions (I215).
- **You are approving:** the answer "yes" for a long-lived token from
  `claude setup-token`, given to Claude Code as
  `CLAUDE_CODE_OAUTH_TOKEN`. With a placeholder there and the proxy
  putting the real token into `Authorization: Bearer`, `-p`,
  `-p --resume` and an interactive session answered. An interactive
  `--resume` opened through the proxy, but nobody asked it a question.
  The real token was in no file of the test user's home and in no
  Keychain item Claude Code made. API key mode wasn't run, because the
  maintainer has no API key. The browser login and its refresh are
  still open in I15.
- **Controls touched:** none weakened. SEC04-no-guest-secrets holds
  for this mode: the guest would hold only the placeholder.
  SEC05-default-deny: the profile can allow `api.anthropic.com` alone
  for the model, and the run shows which other hosts Claude Code
  reaches without it.
- **Assumed or inferred:**
  - That a `setup-token` token lasts long enough that no refresh is
    needed in a session. The run didn't see a refresh call. How
    long the token lasts wasn't measured.
  - That API key mode works end to end, from OpenShell's evidence
    (X00-index). The spike measured only that the placeholder goes out
    as `x-api-key` and is replaced.
  - That the host's own Claude Code and the guest's don't race, because
    they hold different credentials. Inferred: `setup-token` makes a
    separate token, and nothing in the guest refreshes it.
  - That Claude Code's switches for nonessential traffic stop the
    Datadog, event log, update, and changelog requests. The variables
    are in the binary, but the spike didn't run with them set.
  - That the placeholder went nowhere but `api.anthropic.com`. The
    proxy looked for it only in `Authorization` and `x-api-key`, not
    in other headers, query strings or bodies, which is where
    S07-06-requests-and-credentials, "Placeholder binding", also looks for a leak.
  - That the main-loop and side calls to `/v1/messages` are what the
    host table says. Inferred from their timing and `anthropic-beta`
    values.
- **Open decisions:** each has a recommendation in
  B60-model-credential.
  1. Telemetry to Datadog, and Anthropic's own event log on
     `api.anthropic.com`, which receives the token. Recommended: Datadog
     stays outside the profile, as S07-egress-gateway says today, and
     the guest turns telemetry off. Sub-choice: A1 keeps the event log
     path `/api/event_logging/` in the profile, and A2 leaves it out.
     With A2 the host enforces it. Recommended: A2. `/api/eval/` stays in
     (recommended), or is left out the same way.
  2. Updates from `downloads.claude.ai`. Recommended: outside the
     profile, with the updater off in the guest. The image then sets
     the version.
  3. The changelog from `raw.githubusercontent.com`. Recommended:
     outside the profile.
  4. Whether the long-lived token becomes a supported subscription
     mode, with its unknown lifetime, its rotation when the host's
     secret store leaks, and its binding to `api.anthropic.com` alone.
     Recommended: yes, for V1, until I15 settles the browser login.
- **Brief:** B60-model-credential

## Question

From X00-index: does Claude Code work with a placeholder credential
while `wb-proxyd` injects the real one, for each supported
authentication mode, including token refresh? I60 asks it for the
modes that don't need a browser: an API key, and, since the maintainer
signs in through enterprise SSO and has no API key, a long-lived token
from `claude setup-token`.

## Answer

| Mode | Answer | Evidence |
|---|---|---|
| Long-lived subscription token (`claude setup-token`, `CLAUDE_CODE_OAUTH_TOKEN`) | **yes** | measured, below |
| API key (`ANTHROPIC_API_KEY`) | open | not run: no API key. Moved to I253. OpenShell's Claude Code provider shows an API key works this way (X00-index, S09-04-credentials, "Model credential") |
| Browser login (`/login`, SSO) and its token refresh | **yes, with conditions** | measured 2026-10-15, "Browser login (I15)" |

**Yes for the long-lived token.** Claude Code 2.1.289 sends the
placeholder as `Authorization: Bearer <placeholder>`, only to
`api.anthropic.com`, with `anthropic-beta: oauth-2025-04-20`. The proxy
replaced the placeholder on all 36 requests that held it, and every
model call returned 200.

| Step | Result |
|---|---|
| `claude -p` | `pong`, `is_error: false`, 1.8 s |
| `claude -p --resume <session>` | `I replied with "pong".`, same session |
| `claude`, interactive | no key prompt. One question asked, and answered |
| `claude --resume <session>`, interactive | opened the session full screen. The maintainer left without a question, so no second answer was seen |

## Measurements

The run was on 2026-10-12, on the host as the test user `wbtest` (I188,
I214), with no VM. Claude Code 2.1.289 had
`HTTPS_PROXY` pointed at a throwaway Go proxy on `127.0.0.1` and
`NODE_EXTRA_CA_CERTS` naming the proxy's CA, which only that process
trusted. The proxy read the token from the Keychain item
`wraithbox-secret` in `wbtest`'s login Keychain. The token and the
placeholder are both 108 characters. The proxy logged method, host,
path, header names and its verdict per request, never a value.

The proxy was set to inject the token on `api.anthropic.com`,
`console.anthropic.com`, `platform.claude.com` and `claude.ai`, to
learn which hosts take it. Only `api.anthropic.com` received it.

`claude setup-token --help`, run by the builder on the host as its own
user with the same Claude Code 2.1.289, prints "Set up a long-lived
authentication token (requires Claude subscription)". It doesn't give a
lifetime.

### Hosts

70 TLS connections, 72 requests. The proxy inspected the connections
to all five hosts. Counts are requests.

| Host | Path | Requests | Token sent | Answer | What it is |
|---|---|---|---|---|---|
| `api.anthropic.com` | `/v1/messages` | 5 | yes | 200 | the model. Inferred from timing and `anthropic-beta`: 3 main-loop calls, one per answered prompt, and 2 side calls |
| | `/api/event_logging/v2/batch` | 10 | yes | 200 | Anthropic's own telemetry |
| | `/api/eval/sdk-zAZezfDKGoZuXXKe` | 4 | yes | 200 | feature flag evaluation (inferred from the path) |
| | `/api/claude_code/policy_limits` | 5 | yes | 200, 304 | the organization's limits |
| | `/api/claude_code/settings` | 5 | yes | 404 | remote managed settings. None are set for this account |
| | `/api/claude_cli/bootstrap` | 7 | yes | 403 | refused every time. Claude Code carried on |
| | `/mcp-registry/v0/servers` | 16 | no | 200 | the public MCP server registry |
| | `/api/hello` | 1 | no | 200 | a reachability check, interactive start |
| `platform.claude.com` | `/v1/oauth/hello` | 1 | no | 200 | a reachability check of the login host, interactive start. No token was refreshed |
| `downloads.claude.ai` | `/claude-code-releases/latest`, then `2.1.296/manifest.json`, `manifest.zst.json`, `manifest.json.raw-sig.json`, `darwin-arm64/claude.zst` | 10 | no | 200 | the auto-updater. It fetched the 2.1.296 build once in each interactive session |
| | `/claude-code-releases/plugins/claude-plugins-official/latest`, then one `.zip` | 2 | no | 200 | the official plugin marketplace |
| `http-intake.logs.us5.datadoghq.com` | `/api/v2/logs` | 5 | no | 202 | Datadog log intake, with Claude Code's built-in `DD-API-KEY` (X22-no-guest-credentials). The fifth was cut off when the run ended |
| `raw.githubusercontent.com` | `/anthropics/claude-code/refs/heads/main/CHANGELOG.md` | 1 | no | 200 | the changelog, shown at interactive start |

`-p` alone reached `api.anthropic.com` and Datadog. The updater,
the plugin marketplace, the changelog, and the two reachability checks
came only with an interactive session. The proxy didn't see a cookie
in any request. It didn't see the placeholder in the `Authorization`
or `x-api-key` header of a request to a host other than
`api.anthropic.com`. It didn't look anywhere else in a request
("Limits").

### What Claude Code stored

- **Keychain:** no item with `claude` or `anthropic` in its service or
  account name. With the token in the environment, Claude Code didn't make
  a credential item.
- **Files:** `~/.claude.json` (51.4 KiB) and, under `~/.claude/`,
  `settings.json`, `history.jsonl`, `policy-limits.json`, an empty
  `remote-settings.json`, and the `projects`, `sessions`, `plugins`,
  `backups` and `cache` directories. No `.credentials.json`.
- **The real token on disk:** a fixed-string search of `wbtest`'s home,
  skipping `~/Library/Keychains`, didn't find a file holding it.

Neither search can be told apart from a failed one in this run. The
token went to `grep -f` through a pipe from `security -w`, and if that
had failed, `grep` would have had no pattern and wouldn't have matched anything. The
Keychain listing hid the errors of `security dump-keychain`. The proxy
read the same item at start, which makes a failed read unlikely, but
the run didn't check it. `run.sh` now fails in both cases (commits
`2a83a3a` and `2801f5f`).

### First run

An earlier run in API key mode found a bug in the spike proxy, not in
Claude Code. The proxy wrote HTTP/2 status lines to its HTTP/1.1
client, and Claude Code failed with `Malformed_HTTP_Response`. It did
show that Claude Code sends a placeholder `ANTHROPIC_API_KEY` as
`x-api-key` to `api.anthropic.com`, and the proxy replaced it there.
The item didn't hold an API key, so `/v1/messages` answered 401.

## Limits

- One run, on one Mac, with one account, with about a minute of
  traffic. The interactive sessions asked one question between them.
- The token's lifetime and a refresh weren't seen. The browser login
  wasn't tried (I15).
- Bodies weren't logged, so what the telemetry requests hold is
  unknown.
- The disk search covered `wbtest`'s home only, not `/tmp` or
  `/var/folders`. It searched for the real token, not the
  placeholder, and didn't check that the token was read ("What Claude
  Code stored").
- The proxy looked for the placeholder in `Authorization` and
  `x-api-key` only. A scan of every header, the query string, and the
  body, as S07-06-requests-and-credentials, "Placeholder binding", does, is still to
  do, in the next run with the maintainer or in the conformance tests
  of `wb-proxyd`.
- The run was on the host, not in a guest, and nothing denied a host:
  what Claude Code does when Datadog, the updater, or the changelog is
  unreachable wasn't measured.
- `/api/claude_cli/bootstrap` refused this token every time. Why
  wasn't checked: the run doesn't show whether another kind of
  credential gets 200 there.

## What it means for the specs

The pull request with this result didn't change them. The maintainer
decided the open points on I60 on 2026-10-12, and I215 made these
changes:

- S07-egress-gateway, model API profile: `api.anthropic.com` with the
  paths above except the event log, and `platform.claude.com`
  `/v1/oauth/hello`. `/api/event_logging/` is a built-in deny.
  Datadog, `downloads.claude.ai` and `raw.githubusercontent.com` are
  outside the profile. "Named places" gains `Authorization` for the
  model API, which every inspected host already removes. The binding
  injects `Authorization: Bearer <token>` for a token, or `x-api-key`
  for an API key, on the four paths of `api.anthropic.com` where
  Claude Code sent the token.
- S09-04-credentials, "Model credential": the long-lived
  token is a supported mode in V1. The placeholder goes in
  `CLAUDE_CODE_OAUTH_TOKEN`, and the text says how the user revokes
  and replaces the token.
- S06-08-projects-and-sessions, "Claude Code in the guest": the guest sets
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `DISABLE_UPDATES=1`,
  and the image sets the Claude Code version. A new version needs a
  cheap image rebuild (I220), and marketplace plugins are unavailable
  in V1.
- FR01-drop-in names those V1 gaps: the updater, the changelog notice
  and marketplace plugins are missing in the guest.
- S15-least-privilege, "Out of scope": the browser login (I15), in
  place of the subscription login.
- S11-verification-and-spikes: X01-model-credential in its list of
  result pages. New conformance cases cover the full-request
  placeholder scan, the event log deny, and a run of Claude Code with
  the left-out hosts refused.

## Browser login (I15)

The run on 2026-10-15, with the maintainer, answers the rest of I15
except API key mode, which moved to I253 because there is no key
to run it with.

### Answer (I15)

**Yes, with conditions.** Claude Code 2.1.296 works on a browser login
when its store holds placeholder access and refresh tokens and a
host-side proxy holds the real pair. The proxy put the real access
token into `Authorization: Bearer`, and answered Claude Code's refresh
itself, so the real refresh token never reached Claude Code.

| Step | Result |
|---|---|
| `claude -p`, store expired | refreshed first, then `pong`, `is_error: false` |
| `claude -p --resume <session>`, store expired again | refreshed with the refresh token the proxy got back, then `I replied with "pong".` |
| `claude`, interactive | one question asked, and answered |

The conditions:

- **Only the host refreshes.** Every refresh returned a new refresh
  token. With a second holder of the same login, each would have to
  learn the other's new token.
- **The login comes from outside the guest.** In the run it came from
  a real `/login` on the host's test user, moved out of Claude Code's
  store by the spike's `swap`.
- **A login lasts about 30 days.** A refresh doesn't move the refresh
  token's deadline. The user signs in again when it passes.

For the question from S14-claude-in-guest: **no**, refusing
`/v1/oauth/hello` doesn't stop the login prompt. Claude Code builds
the authorize URL, with its PKCE challenge, without a request.

### Run (I15)

On the host as `wbtest`, in a `su - wbtest` terminal, with Claude Code
2.1.296 and the spike proxy set up as on 2026-10-12. `login.sh` ran
five steps: `deny`, `observe`, `placeholder`, `restore` and
`nokeychain`, then a `/logout` through the proxy. The maintainer
signed in with the enterprise SSO account that made the long-lived
token of 2026-10-12.

The proxy logged hosts and paths. On a path holding `oauth` or `token`
it logged the body's field names, with values only for fields that
aren't secret (`grant_type`, `client_id`, `scope`, `expires_in` and the
like), and string lengths for the rest. It scanned every header, the
query and the body of every request for the two placeholders.

The first `deny` and `observe` runs are void. `login.sh` set its
`EXIT` trap inside a function, and zsh runs such a trap when the
function returns, so the proxy stopped before Claude Code started.
Claude Code met a closed port, and the login failed. Commit `7f2d9a1`
fixed it, and both steps ran again.

### The login

| What | Measured |
|---|---|
| Authorize | `https://claude.com/cai/oauth/authorize`, with `code=true`, `response_type=code`, `redirect_uri=https://platform.claude.com/oauth/code/callback`, an S256 PKCE challenge, `state`, and the public client ID `9d1c250a-e61b-44d9-88ed-5944d1962f5e`. The browser shows a code, which the user pastes into Claude Code |
| Scopes | asked: `org:create_api_key`, `user:profile`, `user:inference`, `user:sessions:claude_code`, `user:mcp_servers`, `user:file_upload`, `user:plugins`. Granted: all but `org:create_api_key` |
| Reachability check | `GET platform.claude.com/v1/oauth/hello`, 200, after the code was pasted and before the exchange. Seen in one of the two logins (`nokeychain`) |
| Exchange | `POST platform.claude.com/v1/oauth/token`, a JSON body: `grant_type=authorization_code`, `code` (48 characters), `code_verifier` (43), `state` (43), `client_id` and `redirect_uri`. No client secret |
| Tokens | 200: `access_token` (108 characters), `expires_in=28800` (8 hours), `refresh_token` (108), `refresh_token_expires_in` (below), `scope`, `token_type=Bearer`, `token_uuid`, and `account` and `organization` objects |
| After the login | `GET` on `api.anthropic.com`, with the token: `/api/oauth/profile`, `/api/oauth/claude_cli/roles` and `/api/organization/claude_code_first_token_date` |
| Stored in | the login Keychain, item `Claude Code-credentials`: one JSON value whose field `claudeAiOauth` holds `accessToken`, `refreshToken`, `expiresAt` (milliseconds), `scopes`, `subscriptionType` (`enterprise`), `rateLimitTier` and an empty `refreshTokenExpiresAt`. No `.credentials.json` |

### Refresh

`x01-proxy swap` moved the real pair into the Keychain item
`wraithbox-oauth`, and wrote placeholders of the same length into
Claude Code's item, with `expiresAt` an hour in the past.

- **Request.** Claude Code refreshed before its first model call:
  `POST platform.claude.com/v1/oauth/token`, a JSON body with
  `grant_type=refresh_token`, `refresh_token`, `client_id`, and
  `scope` set to the granted list. No client secret.
- **Swap.** The proxy put the real refresh token into the body, and
  the placeholders into the answer in place of the new real tokens.
  Claude Code then stored the placeholders with `expiresAt` 8 hours
  ahead.
- **Rotation.** Both refreshes answered 200, with a new access token
  and a new refresh token each time, and `expires_in=28800`.
- **Fixed deadline.** `refresh_token_expires_in` fell with the clock
  across both refreshes and the restore: 2,472,894 s at 19:42,
  2,472,627 at 19:47 and 2,472,374 at 19:51. The deadline is set at
  login. A new login at 19:54 got 2,579,568 s, about 29.9 days.
- **Old refresh tokens still work.** The proxy failed to save the
  rotated pairs (`security add-generic-password -U` exited 45, a spike
  bug fixed in `5dabd68`), so its item kept the first pair. The
  restore step put that pair back, and Claude Code refreshed with the
  first refresh token at 19:51, about five minutes after it had been
  rotated twice: 200. Rotation doesn't revoke the old refresh token,
  at least not within five minutes.

### Placeholders and the real tokens

- The scan found a placeholder in one place only: the body of the
  refresh request to `platform.claude.com`, where the proxy swaps it.
  No other header, query string, or body had one.
- The proxy checks each token answer for a real token it holds before
  the answer goes to Claude Code, and found none.
- A fixed-string search of `wbtest`'s home for the first real pair,
  outside `~/Library/Keychains`, didn't find a file. The pairs from the two
  refreshes were in the proxy's memory only and weren't searched for.

### Organization plugins and skills

With the browser login, Claude Code also reached these paths on
`api.anthropic.com`, with the token:
`/api/oauth/organizations/<org>/marketplaces`, `plugins/list-plugins`,
`plugins/<id>/download`, `skills/list-skills` and
`skills/<name>/download`. It downloaded nine skills and one plugin of
the maintainer's organization, as zip files of 5 to 173 KB, into
`~/.claude/skills` and `~/.claude/plugins`. The long-lived token of
2026-10-12 reached none of these paths.

### Without a Keychain

In the `su` terminal with the login Keychain locked, `/login` worked,
and Claude Code wrote the login in plaintext to
`~/.claude/.credentials.json` (mode 0600, 528 bytes), without a
message. With the Keychain unlocked again, Claude Code still read the
file, and `/logout` deleted it.

### The authorize URL without a network

Logged out, with the proxy refusing all of `platform.claude.com`,
`claude.ai` and `console.anthropic.com`, `/login` printed the
authorize URL. The proxy saw 23 connections in that session, to
`api.anthropic.com`, `downloads.claude.ai` and Datadog, and none to a
refused host, so it refused no request.

### Logout

`/logout` sent `POST platform.claude.com/v1/oauth/token/revoke`, a
JSON body with `token` (108 characters), `token_type_hint` (13, the
length of `refresh_token`) and `client_id`. It answered 200 with an
empty body. Claude Code then deleted its store and exited. Whether
revoking the newest refresh token also revokes the older ones of the
same login wasn't tested.

### The billing banner

With the restored login, and with the login read from
`.credentials.json`, the banner said "API Usage Billing" where it
otherwise said "Claude Enterprise". The model answered either way.
Why wasn't checked.

### Limits (I15)

- One enterprise SSO account, one Mac, one run, Claude Code 2.1.296. A
  Pro or Max login may differ in scopes, lifetimes, and paths.
- On the host, not in a guest.
- One check of an old refresh token, five minutes after the rotation.
- Claude Code refreshed when `expiresAt` had passed. Whether it also
  refreshes on a 401, or never does when `expiresAt` lies far ahead,
  wasn't tested.
- `/v1/oauth/hello` showed in one login of two.
- The tokens from the two refreshes stayed in the proxy's memory,
  weren't searched for on disk and weren't revoked by name.

### What it means for the specs (I15)

The pull request with this result doesn't change them. The maintainer
decided on I15 on 2026-10-15: the browser login isn't in V1 (I257
keeps the design), and the authorize URL gets no filter. I256 makes
these changes:

- S14-claude-in-guest, "Commands that don't fit a guest": the answer
  to its measurement, the authorize host `claude.com`, and that a
  guest's login fails at the exchange, not before the URL.
- S09-04-credentials, "Model credential": API key mode is
  open in I253, and the browser login isn't a V1 mode.
- S15-least-privilege, "Out of scope": the browser login points to
  I257.
- X00-index: the I15 answer.

## Spike code

Branch `spike/x01-model-credential`, commit
[`2801f5f`](https://github.com/wraithbox/wraithbox/tree/2801f5fdd70c484f49b24e8b73f6923159060fcb/spikes/x01-model-credential):
the proxy, its tests, and `run.sh`. The run on 2026-10-12 used
`run.sh` as of `768cc28`, before the search checks. The logs stay on the maintainer's
Mac. They don't hold a credential.

For I15, commit
[`5dabd68`](https://github.com/wraithbox/wraithbox/tree/5dabd68a15ab809a6256ab09471baf11fdcdd2f6/spikes/x01-model-credential):
`login.sh`, and the proxy's deny rules, field logging, full-request
scan, OAuth store and `swap`. The runs on 2026-10-15 used the proxy
as of `e499d3b` and `login.sh` as of `7f2d9a1`, and the restore step
as of `5dabd68`. Their logs stay on the maintainer's Mac and don't
hold a token.

**Status:** Answered 2026-10-12: yes for a long-lived token. Decided 2026-10-12 (I60): A2, the model only with the event log denied by the host, and the long-lived token as a supported mode for V1. Answered 2026-10-15 (I15): yes, with conditions, for the browser login. Decided 2026-10-15 (I15): not in V1, and no filter for the authorize URL
