# S14-03 - Configuration Filter

**Purpose:** The units, bounds, filters and secret scan that the user's
configuration passes, and how `wb-guestd` writes the copy in the
guest.

## Design

### Units

Each of these is one unit:

- `CLAUDE.md` and `settings.json`;
- each file in `commands/`;
- each entry of `agents/` and of `skills/`: a file, or a directory with
  everything in it.

Every skip on this page skips the whole unit. So a skill or agent never
reaches the guest in part.

### Walk bounds

`wb` walks each root in byte order of the paths. It reads at most
10,000 entries, 16 levels deep, and 64 MiB in all.

It checks a file's size before it reads it. A file over 1 MiB is
skipped unread.

When a bound is reached, the unit being read and the units not yet
read are skipped, with the rule `claude-config-walk-bound`. So a unit
is never cut short.

### Settings filter

`settings.json` keeps only these top-level keys: `permissions`,
`model`, `effortLevel`, `alwaysThinkingEnabled`, `outputStyle`,
`language`, `theme`, `editorMode`, `attribution`,
`includeCoAuthoredBy`, `includeGitInstructions`, `cleanupPeriodDays`,
`autoCompactEnabled`, `respectGitignore`, `spinnerTipsEnabled`,
`verbose` and `skipDangerousModePermissionPrompt`.

`wb` drops the rest. Among them are `hooks`, `env`, `apiKeyHelper`,
`statusLine`, the cloud credential helpers, the MCP server keys, and
the plugin and marketplace keys. A key joins the list by a change to
this spec.

- Each kept key has a fixed JSON type. A value of another type drops
  the key.
- `wb` writes the guest's `settings.json` from the parsed and filtered
  object, never the host's bytes.
- When `settings.json` isn't valid JSON, the guest doesn't get it.
- `permissions.additionalDirectories` holds host paths, and the guest
  sees them. They name directories that don't exist in the guest.
- Custom output styles are in `output-styles/`, which isn't read. When
  `outputStyle` is set and the host has that directory, the session
  summary says that the guest doesn't get the style files.

### Plugin and hook files

These skip their unit:

- a directory that holds `.claude-plugin/`;
- a file named `plugin.json`, `hooks.json`, `.mcp.json` or
  `marketplace.json`;
- a JSON or YAML file whose top level has a `hooks` or `mcpServers`
  key, or that doesn't parse;
- a Markdown file whose front matter has a `hooks` or `mcpServers`
  key, or whose front matter doesn't parse.

Hooks and MCP servers can run commands and hold tokens. V1 carries in
none (S14-claude-in-guest, "Out of scope").

### Secret scan

`wb` scans each file it would send, the filtered `settings.json`
included. It uses the secret scanner of the image seal
(S06-02-image-build, "Sealing"), as it does for the working tree
carry-in (S08-01-into-the-guest, "Carry-in scan").

- A match skips its unit.
- A file the scanner can't decode skips its unit.

When no scanner is configured or chosen, or when the scanner fails or
times out, `wb` sends none of the configuration. The session then
starts without it and says why (SEC04-no-guest-secrets).

### Total cap

`wb` adds the units that pass in byte order of their paths, while the
total stays at most 16 MiB. A unit that would go over is skipped, and
`wb` goes on with the next one.

### Reporting

The session summary lists each skipped unit or dropped key with its
reason. For a scanner match it gives the rule that matched, never the
matched text.

Each path sent and each path skipped goes to `wb-hostd`'s audit
writer, a skip with its rule (SEC10-audit).

### Writing

`wb-hostd` passes the copy to `wb-guestd`. `wb-guestd` writes it in a
child process as the project user (S13-01-process-hardening).

- It replaces each entry in the project user's `~/.claude/` with the
  host's version.
- It removes each entry, or each unit inside one, that the copy
  doesn't have.

So the host is the one source (FR13-claude-state, as narrowed on I45
on 2026-10-12). A unit skipped this time is gone from the guest,
instead of left as an older copy. So is every entry after a scanner
failure.

A change made in the guest to these entries, such as a model picked
with `/model`, lasts until the next session start.

### Guest-only state

`wb-guestd` doesn't touch the rest of the project user's Claude Code
state. So `~/.claude.json`, `~/.claude/projects/` and
`~/.claude/plugins/` in the guest persist per project
(FR13-claude-state).

An MCP server or plugin added in the guest stays in that project's
guest home, for example one added with `claude mcp add -s user`. This
is accepted: it never reaches the host or another project, and its
traffic meets the same egress policy.

### One way

Nothing in the project user's `~/.claude/` returns to the host
(SEC03-no-host-exec). A repository's own `.claude/` returns as work,
and its changes are flagged (S08-workspace-and-git).

## Decisions

### An allowlist of settings keys

An allowlist of keys filters `settings.json`.

- **Rejected:** a list of removed keys.
- **Why:** Claude Code adds settings keys often. A new key that runs a
  command or holds a credential then stays on the host.

**Status:** Draft
