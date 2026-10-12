# S14 - Claude Code in the Guest

**Purpose:** How Claude Code gets into the guest image, which of the
user's Claude Code configuration goes with it, how it starts, and the
agent interface that keeps Claude Code's specifics in one place
(V1-02-claude-only).

**Requirements:** FR01-drop-in, FR13-claude-state, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC05-default-deny, SEC07-dep-gate, SEC10-audit.

Brief: B45-claude-in-guest

## Design

![Claude Code in the guest. wb reads an allowlist of the user's ~/.claude/, filters and scans it, and wb-hostd hands the copy to wb-guestd, which writes it as the project user. ~/.claude.json never reaches the guest. The download program fetches a Claude Code build on the host, wb-hostd checks it, and wb-guestd installs it root-owned during the image build. Claude Code reaches Anthropic only through wb-proxyd, which injects the model credential.](S14-claude-in-guest.svg)

The maintainer decided rows 1, 2, 3 and 6 of B45-claude-in-guest on
I45 on 2026-10-04. Rows 4 and 5 follow from the decisions on I60
(B60-model-credential), as far as those reach.

### Agent interface

Claude Code is the only agent in V1, and the interface is what Claude
Code needs, with no general design before a second agent exists
(decided on I45, row 6). Agent-specific code is in one Go package per
agent, `internal/agent/claude`, and `wb`, `wb-hostd` and `wb-guestd`
call it only through the interface in `internal/agent`. An agent supplies:

- **Command.** The agent command name for `wb` (`claude`, S05-cli,
  "Agent commands") and the program `wb-guestd` starts.
- **Install.** How the image build gets and checks the agent's build
  ("Install" below).
- **Configuration carry-in.** Which of the user's files reach the
  guest, and the filter they pass ("User configuration" below).
- **Launch.** The environment variables `wb-guestd` adds to the
  session's allowlist, and the state the agent keeps per project
  (S06-vm-lifecycle, "Claude Code in the guest" and "Projects and
  sessions inside a VM").
- **Credential bindings.** The kinds of model credential, the guest
  variable that holds each placeholder, and the header each binding
  injects (S09-policy-credentials-audit, "Model credential").
- **Endpoint profile.** The built-in HTTP profile the agent needs, the
  model API profile for Claude Code (S07-egress-gateway, "HTTP
  policy").

### Install

Only the host downloads Claude Code (decided on I45, row 1). The guest
never fetches it, and `downloads.claude.ai` is outside every profile
the guest gets (S07-egress-gateway, model API, "Outside the profile").

- **Download.** During `wb image build`, the download program of I49,
  which `wb-hostd` starts, fetches a release's `manifest.json`, its
  detached signature `manifest.json.sig`, and the guest's binary from
  `https://downloads.claude.ai/claude-code-releases/<version>/`. The
  same program fetches restore images (decided on I49), so `wb-hostd`
  itself doesn't open an outbound connection, and Wraith Box has one
  downloader.
- **Check.** `wb-hostd` checks the manifest's signature against
  Anthropic's Claude Code release key, which Wraith Box carries with
  fingerprint `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`, and
  the binary's SHA-256 against the manifest's checksum for the guest's
  platform (`darwin-arm64` for a macOS guest). It never fetches the
  key. Any failed check fails the build, and the build logs the check
  that failed. Releases before 2.1.89 have no
  signature, and the build refuses them.
- **Into the image.** `wb-guestd` installs the binary through a named
  operation of the base layer, before sealing (S06-vm-lifecycle,
  "Built by Wraith Box"), as `/usr/local/bin/claude`, owned by root
  with mode 755, in a directory that project users can't write to. The
  toolchain user can't write it either, so a project's formulae can't
  replace it (S06-vm-lifecycle, "Toolchain prefix"). `wb-guestd`
  starts it by that absolute path, never through `PATH`.
- **Version.** The image sets the Claude Code version, and the guest
  can't change it: `DISABLE_UPDATES=1` blocks every update path
  (S06-vm-lifecycle, "Claude Code in the guest"), and the profile is
  the control. An updater would fetch and run a binary past
  SEC07-dep-gate. A new version arrives with a new image. Which release
  a build picks, and how a build for a new Claude Code version costs
  less than a full build, is I220. `wb image list` shows each image's Claude Code
  version.
- **Version skew.** The host's Claude Code and the guest's can differ,
  and nothing compares them. Only configuration files cross ("User
  configuration"), and how the guest's build treats a key it doesn't
  know is up to that build.

### Permission mode

`wb-guestd` doesn't write `permissions.defaultMode`, and Wraith Box
adds no `--permission-mode` (decided on I45, row 2). The mode comes from
the user's `settings.json` in the copy below, else from Claude Code's own
default, and `--permission-mode` after `wb claude` overrides both,
because `wb` passes it unchanged (S05-cli). A user who runs
`bypassPermissions` on the host gets it in the guest. Claude Code
refuses that mode as root, and project users are never root
(S13-guest-confinement, "Layer 1"). Rejected: defaulting to
`bypassPermissions`, which changes Claude Code's behavior behind the
user's settings and breaks FR01-drop-in.

### User configuration

At each session start, `wb` filters and scans an allowlist of the
user's `~/.claude/` and copies it into the project user's home
(decided on I45, row 3, option A). `wb-hostd` can read only its state
directories (S04-architecture, "Each host daemon is self-sandboxed"),
so `wb`, which runs as the user, reads the files. It doesn't read a file the
guest wrote.

- **What is read.** `CLAUDE.md`, `settings.json`, and the directories
  `agents/`, `skills/` and `commands/`, under `~/.claude/` or
  `CLAUDE_CONFIG_DIR` when the user's environment sets it. `wb` follows
  symbolic links, as Claude Code on the host does, because users link
  these directories from their own repositories, and the guest gets
  regular files only. A link loop, or anything other than a regular
  file or a directory, is skipped. Never read: `~/.claude.json`, which
  holds the sign-in session and MCP server definitions, `plugins/`,
  `hooks/`, `projects/`, history, and everything else in `~/.claude/`.
- **Settings filter.** `settings.json` keeps only these top-level keys:
  `permissions`, `model`, `effortLevel`, `alwaysThinkingEnabled`,
  `outputStyle`, `language`, `theme`, `editorMode`, `attribution`,
  `includeCoAuthoredBy`, `includeGitInstructions`, `cleanupPeriodDays`,
  `autoCompactEnabled`, `respectGitignore`, `spinnerTipsEnabled`,
  `verbose` and `skipDangerousModePermissionPrompt`. Every other key is
  dropped, among them `hooks`, `env`, `apiKeyHelper`, `statusLine`, the
  cloud credential helpers, the MCP server keys, and the plugin and
  marketplace keys. An allowlist and not a list of removed keys,
  because Claude Code adds settings keys often, and a new key that runs
  a command or holds a credential then stays on the host. A key joins
  the list by a change to this spec. When `settings.json` isn't valid JSON,
  the guest doesn't get it.
- **Front matter.** A file in `agents/`, `skills/` or `commands/` whose
  front matter has a `hooks` or `mcpServers` key is skipped whole, for
  the same reason as the settings filter.
- **Secret scan.** `wb` scans each file it would send, the filtered
  `settings.json` included, with the secret scanner of the image seal
  (S06-vm-lifecycle, "Sealing"), as it scans the working tree carry-in
  (S08-workspace-and-git, "Carry-in scan"). A match skips the file.
  When no scanner is configured or chosen, or the scanner fails or
  times out, no configuration reaches the guest, and the session
  starts without it and says why (SEC04-no-guest-secrets).
- **Caps.** A file over 1 MiB is skipped. When the files that pass
  come to more than 16 MiB, `wb` sends them in path order up to that
  total and skips the rest.
- **Reporting.** The session summary lists each skipped file or dropped
  key with its reason, and for a scanner match the rule that matched,
  never the matched text. Each decision goes to `wb-hostd`'s audit
  writer with its rule (SEC10-audit).
- **Writing.** `wb-hostd` passes the copy to `wb-guestd`, which writes
  it in a child process as the project user (S13-guest-confinement,
  "Layer 1"). It replaces each allowlisted path in the project user's
  `~/.claude/` with the host's version, and removes the path when the
  host has none, so the host is the one source. A change made in the
  guest to these paths, such as a model picked with `/model`, lasts
  until the next session start. Claude Code's state in the guest,
  `~/.claude.json` and `~/.claude/projects/` included, isn't touched and
  stays per project (FR13-claude-state).
- **One way.** Nothing in the project user's `~/.claude/` returns to
  the host (SEC03-no-host-exec). A repository's own `.claude/` returns
  as work, and its changes are flagged (S08-workspace-and-git).

Rejected: an empty configuration per project, which makes `wb claude`
unlike `claude` (FR01-drop-in), and a copy of `~/.claude/` and
`~/.claude.json` as they are, which puts the sign-in session and MCP
server tokens in the guest (SEC04-no-guest-secrets).

### Commands that don't fit a guest

Settled by the decisions on I60 and SEC05-default-deny:

- **`claude update` and `claude install`** fail, because
  `DISABLE_UPDATES` blocks them and `downloads.claude.ai` is outside
  the profile. The version comes with the image ("Install").
- **`/plugin install` and the plugin marketplace** fail for the same
  host, a known FR01-drop-in gap in V1 (S06-vm-lifecycle, "Claude Code
  in the guest").
- **`/ide`** can't reach an editor on the host, because the guest has
  no route to the host or the LAN (SEC05-default-deny), and V1 has no
  port forwarding (V1-08-no-forward-clipboard).

Deferred to I15: `/login`, `claude login`, `/logout` and
`claude setup-token` run inside the guest. The model credential in V1
is a placeholder in the session's environment
(S09-policy-credentials-audit, "Model credential"). The model API
profile doesn't allow a login or token path, only the reachability check
`/v1/oauth/hello` on `platform.claude.com`, so a login started in the
guest fails at `wb-proxyd`. What `wb` says when it does, and
whether the browser login gets a host-side flow, is part of I15.

### Telemetry and other endpoints

Settled by the decisions on I60: the model API profile is the list of
what Claude Code in the guest may reach (S07-egress-gateway, model
API). Anthropic's event log is a built-in deny, and Datadog, the
updater and the changelog are outside the profile. The guest sets
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` and `DISABLE_UPDATES=1`
(S06-vm-lifecycle, "Claude Code in the guest"), and the profile, not
the guest's environment, is the control.

Deferred to I15: the endpoints of API key mode beyond the placeholder
on `api.anthropic.com`, and those of the browser login and its token
refresh, which X01-model-credential didn't measure.

## Out of scope

- Carrying in a hook or an MCP server the user names. V1 carries none.
- Plugins, and `keybindings.json`, output styles, and status line
  scripts in `~/.claude/`.
- A project's own `.mcp.json` and `.claude/` in the repository. They
  reach the guest as part of the work, and Claude Code treats them as
  project configuration.
- Agents other than Claude Code (V1-02-claude-only).

**Status:** Draft
