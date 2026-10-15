# S14-04 - Commands and Endpoints

**Purpose:** Which permission mode Claude Code runs in, the commands
that don't work in a guest, and the endpoints Claude Code in the guest
may reach.

## Design

### Permission mode

`wb-guestd` doesn't write `permissions.defaultMode`. Wraith Box doesn't
add `--permission-mode`.

The mode comes from the user's `settings.json` in the copy
(S14-02-user-configuration), else from Claude Code's own default.
`--permission-mode` after `wb claude` overrides both, because `wb`
passes it unchanged (S05-cli).

A user who runs `bypassPermissions` on the host gets it in the guest.
Claude Code refuses that mode as root, and project users are never
root (S13-01-process-hardening).

### Commands that don't fit a guest

The decisions on I60 and SEC05-default-deny settle these:

- **`claude update` and `claude install`** fail. `DISABLE_UPDATES`
  blocks them, and `downloads.claude.ai` is outside the profile. The
  version comes with the image (S14-01-install).
- **`/plugin install` and the plugin marketplace** fail for the same
  reason. That is a known FR01-drop-in gap in V1
  (S06-08-projects-and-sessions, "Claude Code in the guest").
- **`/ide`** can't reach an editor on the host. The guest has no route
  to the host or the LAN (SEC05-default-deny), and V1 has no port
  forwarding (V1-08-no-forward-clipboard).

### Browser login

The browser login isn't a V1 model credential (B15-browser-login). The
model credential in V1 is a placeholder in the session's environment
(S09-04-credentials, "Model credential").

`/login`, `claude login`, `/logout` and `claude setup-token` start in
the guest. Each fails at its token request.

The model API profile allows the reachability check `/v1/oauth/hello`
on `platform.claude.com`. It doesn't allow a login or token path
(S07-07-http-policy, "model API"). `wb-proxyd` refuses:

- the code exchange, `POST platform.claude.com/v1/oauth/token`, where
  `/login`, `claude login` and `claude setup-token` fail;
- the revocation, `POST platform.claude.com/v1/oauth/token/revoke`,
  where `/logout` fails.

Refusing `/v1/oauth/hello` wouldn't stop the login prompt
(X01-model-credential, "Browser login (I15)"):

- Claude Code builds the authorize URL on `claude.com`
  (`/cai/oauth/authorize`) without a request.
- X01-model-credential saw `/v1/oauth/hello` in one login of two.
  There the call came between the pasted code and the exchange.

#### Residual

Claude Code in the guest can still print a real authorize URL, with
its PKCE challenge.

A user can open it, sign in on the host, and paste the code into the
guest. That gives the guest a code and its verifier. The guest can't
redeem them through `wb-proxyd`. But it can send them out through a
channel that policy allows, and they can be redeemed elsewhere.

#### Warning

`wb` tells the user never to paste a login or `setup-token` code into
a guest. It says so at a project's first session and in `wb help`.

The terminal stream doesn't filter the authorize URL
(B15-browser-login).

### Telemetry and other endpoints

The model API profile is the list of what Claude Code in the guest may
reach (S07-07-http-policy, "model API").

- Anthropic's event log is a built-in deny.
- Datadog, the updater and the changelog are outside the profile.

The guest sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and
`DISABLE_UPDATES=1` (S06-08-projects-and-sessions, "Claude Code in the guest"). The profile is the control, not the guest's environment.

## Decisions

### Keep the user's permission mode

Wraith Box doesn't set a permission mode of its own.

- **Rejected:** defaulting to `bypassPermissions`. It changes Claude
  Code's behavior behind the user's settings, and breaks FR01-drop-in.
- **Decided:** I45, row 2.

### The model API profile lists every endpoint

- **Decided:** I60.

## Out of scope

- A host-side browser login, in which `wb-proxyd` holds the real tokens
  and answers Claude Code's refresh. It comes after V1 (I257).
- The endpoints of API key mode beyond the placeholder on
  `api.anthropic.com`. They are deferred to I253.

**Status:** Draft
