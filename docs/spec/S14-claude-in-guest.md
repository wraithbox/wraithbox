# S14 - Claude Code in the Guest

**Purpose:** How Claude Code gets into the guest image, which of the
user's Claude Code configuration goes with it, how it starts, and the
agent interface that keeps Claude Code's specifics in one place
(V1-02-claude-only).

**Requirements:** FR01-drop-in, FR13-claude-state, SEC02-no-host-fs-share, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC05-default-deny, SEC07-dep-gate, SEC10-audit, SEC12-least-privilege.

Brief: B45-claude-in-guest

## Design

![Claude Code in the guest. wb reads an allowlist of the user's ~/.claude/, filters and scans it, and wb-hostd hands the copy to wb-guestd, which writes it as the project user. ~/.claude.json never reaches the guest. The download program fetches a Claude Code build on the host, wb-release-check checks it for wb-hostd, and wb-guestd installs it root-owned during the image build. Claude Code reaches Anthropic only through wb-proxyd, which injects the model credential.](S14-claude-in-guest.svg)

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

- **Download.** During `wb image build`, the download program that the
  I49 decision adds fetches a release's `manifest.json`, its detached
  signature `manifest.json.sig`, and the guest's binary from
  `https://downloads.claude.ai/claude-code-releases/<version>/`. The
  binary is at `<version>/<platform>/<binary>`, where `<binary>` is the
  platform entry's `binary` field (`claude` for `darwin-arm64`): one
  uncompressed Mach-O executable, signed by Anthropic PBC and notarized
  by Apple. `wb-hostd` itself doesn't open an outbound connection. No
  spec defines the download program yet (it isn't in S04-architecture),
  and this fetch depends on the spec that I49 asks for.
- **Check.** `wb-release-check` (S04-architecture), a per-run program
  that `wb-hostd` starts through `wb-launcher`, checks the files on
  inherited descriptors and writes a fixed-format record to a pipe:
  `pass` with the manifest's signed SHA-256 checksum for the guest's
  platform, or the rule of the failed check. `wb-hostd`
  doesn't parse the signature or the manifest. They are bytes from the
  network, and the process that faces the guest doesn't run a third
  party parser over them (SEC12-least-privilege). The check follows
  Anthropic's "Binary integrity and code signing"
  (https://code.claude.com/docs/en/setup):
  - the detached signature verifies over `manifest.json` with
    Anthropic's Claude Code release key. The full public key is compiled
    into Wraith Box, and its fingerprint is
    `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`. Wraith Box
    never downloads the key;
  - the manifest's `version` equals the version the build asked for, so
    an older signed release served under a newer path fails;
  - the version is at least the minimum compiled into Wraith Box,
    2.1.289, the Claude Code version that X01-model-credential measured
    the model API profile with.
    Releases before 2.1.89 have no signature, and this minimum refuses
    them too;
  - the manifest has an entry for the guest's platform (`darwin-arm64`
    for a macOS guest), and the binary's size and SHA-256 equal that
    entry's `size` and `checksum`.

  Any failed check fails the build, and the build logs the check that
  failed. The manifest parser has a fuzz target.
- **The checked bytes.** `wb-hostd` opens the binary once and keeps
  that descriptor. It first `fstat`s it, and refuses a file that isn't
  a regular file with the rule `claude-release-bytes-changed`. It
  passes a duplicate to `wb-release-check`, and after a `pass` it reads
  the file with `pread` from offset 0, because the duplicate shares the
  child's file offset, and streams it to `wb-guestd`, hashing it as it
  sends. It sends the SHA-256 from the
  record only after the last byte. `wb-guestd` writes the stream to a
  temporary file that only root can write, and installs it only when
  the SHA-256 of what it received equals the one `wb-hostd` sent and
  `wb-hostd` reports that its own hash of the sent bytes matched too.
  A file changed between the check and the send fails the build with
  the rule `claude-release-bytes-changed`.
- **Into the image.** `wb-guestd` installs the binary through a named
  operation of the base layer, before sealing (S06-vm-lifecycle,
  "Built by Wraith Box"), as `/usr/local/bin/claude`, owned by root
  with mode 755, in a directory that project users can't write to. The
  toolchain user can't write it either, so a project's formulae can't
  replace it (S06-vm-lifecycle, "Toolchain prefix"). `wb-guestd`
  starts it by that absolute path, never through `PATH`.
- **Version.** The image sets the Claude Code version, and the
  installed binary can't change: project users can't write it, and
  `DISABLE_UPDATES=1` blocks every update path (S06-vm-lifecycle,
  "Claude Code in the guest"). The profile is the control. An agent
  can still install another Claude Code in its own home, for example
  with `npm`, and `wb-guestd` doesn't start that one. An updater would
  fetch and run a binary past SEC07-dep-gate. A new version arrives
  with a new image. I220 decides which release a build picks, and how
  a build for a new Claude Code version costs less than a full build.
  `wb image list` shows each image's Claude Code version.
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
so `wb`, which runs as the user, reads the files. They can come from a
repository the user merged the guest's work into, so `wb` treats them as
untrusted input: its settings and front matter parsers have fuzz
targets.

- **Config root.** `~/.claude/`, or `CLAUDE_CONFIG_DIR` when the
  user's environment sets it.
- **Entries.** `CLAUDE.md`, `settings.json`, and the directories
  `agents/`, `skills/` and `commands/` of the config root. Never read:
  `.claude.json` in the home or in `CLAUDE_CONFIG_DIR`, which holds the
  sign-in session and MCP server definitions, `plugins/`, `hooks/`,
  `projects/`, `output-styles/`, history, and everything else in the
  config root.
- **Symbolic links** (decided on I45 on 2026-10-12, SEC02-no-host-fs-share).
  - *Each entry once.* `wb` resolves each entry's path once, following
    links, to get that entry's root. So a `skills` directory
    linked into a repository works, as it does for Claude Code on the
    host.
  - *Inside a root.* A link whose target resolves outside its root is
    skipped with the rule `claude-config-link-outside`, and `wb` doesn't
    read the target.
  - *Never `.claude.json`.* A root or a link that resolves to
    `.claude.json` in the home or in `CLAUDE_CONFIG_DIR` is skipped with
    the rule `claude-config-link-claude-json`, unread.
  - *Roots `wb` refuses.* A root is skipped unread, with the rule
    `claude-config-root-refused`, when its resolved path is the home
    directory or one of its ancestors, has a component outside the
    config root that starts with `.` (such as `~/.ssh` or
    `~/.config/gh`), or lies under the platform's user data directory
    (`~/Library` on macOS, `%APPDATA%` and `%LOCALAPPDATA%` on
    Windows). Those hold credentials and application data, not Claude
    Code configuration.
  - *Why this differs from S08.* The working tree carry-in sends a link
    as a link and never reads its target (S08-workspace-and-git,
    "Carry-in scan"), because the guest gets the same repository and
    needs only the link. A configuration entry is useless as a dangling
    link in the guest, and users link these entries on purpose, so `wb`
    follows the one link the user made at the top and no link below it
    that leaves the root.
  - *Residual.* Inside a root, a link to another file of the same root
    is followed. When the root is in a repository the user merged
    the guest's work into, a link the agent committed can name a file of that
    repository the guest never got, such as an ignored `.env`. Only the
    secret scan stands between that file and the guest.
  - Anything other than a regular file, a directory, or a link is
    skipped. A link loop is skipped.
  - *Hidden entries.* Inside a root, `wb` skips each entry whose name
    starts with `.`, such as `.git` or `.env`, and doesn't read it. The
    one exception is `.claude-plugin/`, which skips its whole unit
    ("Plugin and hook files").
- **Units.** `CLAUDE.md`, `settings.json`, each file in `commands/`,
  and each entry of `agents/` and of `skills/` (a file, or a directory
  with everything in it) is one unit. Every skip below skips the whole
  unit, so a skill or agent never reaches the guest in part.
- **Walk bounds.** `wb` walks each root in byte order of the paths,
  and reads at most 10,000 entries, 16 levels deep, and 64 MiB in all.
  It checks a file's size before it reads it, and a file over 1 MiB is
  skipped unread. When a bound is reached, the unit being read and the
  units not yet read are skipped with the rule
  `claude-config-walk-bound`, so a unit is never cut short.
- **Settings filter.** `settings.json` keeps only these top-level keys:
  `permissions`, `model`, `effortLevel`, `alwaysThinkingEnabled`,
  `outputStyle`, `language`, `theme`, `editorMode`, `attribution`,
  `includeCoAuthoredBy`, `includeGitInstructions`, `cleanupPeriodDays`,
  `autoCompactEnabled`, `respectGitignore`, `spinnerTipsEnabled`,
  `verbose` and `skipDangerousModePermissionPrompt`. `wb` drops the
  rest, among them `hooks`, `env`, `apiKeyHelper`, `statusLine`, the
  cloud credential helpers, the MCP server keys, and the plugin and
  marketplace keys. An allowlist and not a list of removed keys,
  because Claude Code adds settings keys often, and a new key that runs
  a command or holds a credential then stays on the host. A key joins
  the list by a change to this spec.
  - Each kept key has a fixed JSON type, and a value of another type
    drops the key. `wb` writes the guest's `settings.json` from the
    parsed and filtered object, never the host's bytes.
  - When `settings.json` isn't valid JSON, the guest doesn't get it.
  - `permissions.additionalDirectories` holds host paths, and the guest
    sees them. They name directories that don't exist in the guest.
  - Custom output styles are in `output-styles/`, which isn't read.
    When `outputStyle` is set and the host has that directory, the
    session summary says that the guest doesn't get the style files.
- **Plugin and hook files.** These skip their unit:
  - a directory that holds `.claude-plugin/`;
  - a file named `plugin.json`, `hooks.json`, `.mcp.json` or
    `marketplace.json`;
  - a JSON or YAML file whose top level has a `hooks` or `mcpServers`
    key, or that doesn't parse;
  - a Markdown file whose front matter has a `hooks` or `mcpServers`
    key, or whose front matter doesn't parse.

  Hooks and MCP servers can run commands and hold tokens, and V1
  carries in none (Out of scope).
- **Secret scan.** `wb` scans each file it would send, the filtered
  `settings.json` included, with the secret scanner of the image seal
  (S06-vm-lifecycle, "Sealing"), as it scans the working tree carry-in
  (S08-workspace-and-git, "Carry-in scan"). A match, or a file the
  scanner can't decode, skips its unit. When no scanner is configured
  or chosen, or the scanner fails or times out, no configuration
  reaches the guest, and the session starts without it and says why
  (SEC04-no-guest-secrets).
- **Total cap.** `wb` adds the units that pass in byte order of their
  paths while the total stays at most 16 MiB. A unit that would go over
  is skipped, and `wb` goes on with the next one.
- **Reporting.** The session summary lists each skipped unit or dropped
  key with its reason, and for a scanner match the rule that matched,
  never the matched text. Each path sent and each path skipped goes to
  `wb-hostd`'s audit writer, a skip with its rule (SEC10-audit).
- **Writing.** `wb-hostd` passes the copy to `wb-guestd`, which writes
  it in a child process as the project user (S13-guest-confinement,
  "Layer 1"). It replaces each entry in the project user's `~/.claude/`
  with the host's version, and removes each entry, or each unit inside
  one, that the copy doesn't have. So the host is the one source
  (FR13-claude-state, as narrowed on I45 on 2026-10-12), and a unit
  skipped this time, or every entry after a scanner failure, is gone
  from the guest instead of left as an older copy. A change made in the
  guest to these entries, such as a model picked with `/model`, lasts
  until the next session start.
- **Guest-only state.** `wb-guestd` doesn't touch the rest of the
  project user's Claude Code state, so `~/.claude.json`,
  `~/.claude/projects/` and `~/.claude/plugins/` in the guest persist
  per project (FR13-claude-state). An MCP server or plugin added in
  the guest, with `claude mcp add -s user` for example, stays in that
  project's guest home. This is accepted: it never reaches the host or
  another project, and its traffic meets the same egress policy.
- **One way.** Nothing in the project user's `~/.claude/` returns to
  the host (SEC03-no-host-exec). A repository's own `.claude/` returns
  as work, and its changes are flagged (S08-workspace-and-git).

Rejected: an empty configuration per project, which makes `wb claude`
unlike `claude` (FR01-drop-in), and a copy of `~/.claude/` and
`~/.claude.json` as they are, which puts the sign-in session and MCP
server tokens in the guest (SEC04-no-guest-secrets).

### Commands that don't fit a guest

The decisions on I60 and SEC05-default-deny settle these:

- **`claude update` and `claude install`** fail, because
  `DISABLE_UPDATES` blocks them and `downloads.claude.ai` is outside
  the profile. The version comes with the image ("Install").
- **`/plugin install` and the plugin marketplace** fail for the same
  reason, a known FR01-drop-in gap in V1 (S06-vm-lifecycle, "Claude
  Code in the guest").
- **`/ide`** can't reach an editor on the host, because the guest has
  no route to the host or the LAN (SEC05-default-deny), and V1 has no
  port forwarding (V1-08-no-forward-clipboard).

Deferred to I15: `/login`, `claude login`, `/logout` and
`claude setup-token` run inside the guest. The model credential in V1
is a placeholder in the session's environment
(S09-policy-credentials-audit, "Model credential"). The model API
profile allows the reachability check `/v1/oauth/hello` on
`platform.claude.com` and no login or token path, so the guest's
request to redeem a login code fails at `wb-proxyd`.

- **Residual.** Claude Code in the guest can still print a real
  authorize URL, with its PKCE challenge. A user who opens it, signs in
  on the host and pastes the code into the guest gives the guest a code
  and its verifier. The guest can't redeem them through `wb-proxyd`,
  but it can send them out through a channel that policy allows, and
  they can be redeemed elsewhere.
- **Warning.** `wb` tells the user never to paste a login or
  `setup-token` code into a guest, at a project's first session and in
  `wb help`. I15 measures whether refusing `/v1/oauth/hello` stops the
  login prompt.

What `wb` says when a login fails, and whether the browser login gets
a host-side flow, is part of I15.

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
