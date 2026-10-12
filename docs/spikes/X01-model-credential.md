# X01 - Model credential via the proxy

**Purpose:** The result of the spike for I60, the part of I15 that
doesn't need a browser login. Does Claude Code work with a placeholder
credential while a host-side proxy injects the real one, and which
hosts does it contact, for the model API profile of
S07-egress-gateway?

**Requirements:** SEC04-no-guest-secrets, SEC05-default-deny,
SEC10-audit, FR01-drop-in, FR13-claude-state

**Brief:** B60-model-credential

## For review

- **Decides:** the answer for a long-lived subscription token, the
  host list for the model API profile, and three questions about the
  hosts outside it. The spec changes wait for those decisions (I215).
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
    Datadog, update, and changelog requests. The variables are in the
    binary, but the spike didn't run with them set.
- **Open decisions:** each has a recommendation in
  B60-model-credential.
  1. Telemetry to Datadog, and Anthropic's own event logging.
     Recommended: Datadog stays outside the profile, as
     S07-egress-gateway says today, and the guest turns telemetry off.
  2. Updates from `downloads.claude.ai`. Recommended: outside the
     profile, with the updater off in the guest. The image then sets
     the version.
  3. The changelog from `raw.githubusercontent.com`. Recommended:
     outside the profile.
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
| API key (`ANTHROPIC_API_KEY`) | open | not run: no API key. OpenShell's Claude Code provider shows an API key works this way (X00-index, S09-policy-credentials-audit, "Model credential") |
| Browser login (`/login`, SSO) and its token refresh | open | I15, with the maintainer |

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

### Hosts

70 TLS connections, 72 requests. The proxy inspected the connections
to all five hosts. Counts are requests.

| Host | Path | Requests | Token sent | Answer | What it is |
|---|---|---|---|---|---|
| `api.anthropic.com` | `/v1/messages` | 5 | yes | 200 | the model. 3 main-loop calls (one per answered prompt) and 2 side calls |
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
in any request, or the placeholder in a request to a host other than
`api.anthropic.com`.

### What Claude Code stored

- **Keychain:** no item with `claude` or `anthropic` in its service or
  account name. With the token in the environment, Claude Code didn't make
  a credential item.
- **Files:** `~/.claude.json` (52 KiB) and, under `~/.claude/`,
  `settings.json`, `history.jsonl`, `policy-limits.json`, an empty
  `remote-settings.json`, and the `projects`, `sessions`, `plugins`,
  `backups` and `cache` directories. No `.credentials.json`.
- **The real token on disk:** a fixed-string search of `wbtest`'s home,
  skipping `~/Library/Keychains`, didn't find a file holding it.

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
  placeholder.
- The run was on the host, not in a guest, and nothing denied a host:
  what Claude Code does when Datadog, the updater, or the changelog is
  unreachable wasn't measured.
- `/api/claude_cli/bootstrap` refused this token every time. Why
  wasn't checked: the run doesn't show whether another kind of
  credential gets 200 there.

## What it means for the specs

Not changed in this pull request. They wait for the open decisions,
and I215 tracks the change:

- S07-egress-gateway, model API profile: `api.anthropic.com` with the
  paths above, and `platform.claude.com` `/v1/oauth/hello`. "Named
  places" gains `Authorization` for the model API, which every
  inspected host already removes. The binding injects
  `Authorization: Bearer <token>` for a token, or `x-api-key` for an
  API key, on `api.anthropic.com` only. Datadog, `downloads.claude.ai`
  and `raw.githubusercontent.com` per decisions 1 to 3.
- S09-policy-credentials-audit, "Model credential": the long-lived
  token is a supported mode, and the placeholder goes in
  `CLAUDE_CODE_OAUTH_TOKEN`.
- S11-verification-and-spikes: X01-model-credential in its list of
  result pages. Its conformance cases already cover a replaced model
  credential and a placeholder outside its binding.

## Spike code

Branch `spike/x01-model-credential`, commit
[`21d5892`](https://github.com/wraithbox/wraithbox/tree/21d5892e957c5b922a3296297c0f6fc13bf3c1f1/spikes/x01-model-credential):
the proxy, its tests, and `run.sh`. The logs stay on the maintainer's
Mac. They don't hold a credential.

**Status:** Partly answered 2026-10-12: yes for a long-lived subscription token. API key mode and the browser login are open (I15)
