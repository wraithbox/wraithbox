# S14-02 - User Configuration

**Purpose:** Which of the user's Claude Code configuration `wb` reads at session
start, and how it treats symbolic links in it.

## Design

At each session start, `wb` filters and scans an allowlist of the
user's `~/.claude/` and copies it into the project user's home
(decided on I45, row 3, option A). `wb-hostd` can read only its state
directories (S04-03-self-sandboxing, "Each host daemon is self-sandboxed"),
so `wb`, which runs as the user, reads the files. They can come from a
repository the user merged the guest's work into, so `wb` treats them as
untrusted input: its settings and front matter parsers have fuzz
targets.

### Config root

`~/.claude/`, or `CLAUDE_CONFIG_DIR` when the
user's environment sets it.

### Entries

`CLAUDE.md`, `settings.json`, and the directories
`agents/`, `skills/` and `commands/` of the config root. Never read:
`.claude.json` in the home or in `CLAUDE_CONFIG_DIR`, which holds the
sign-in session and MCP server definitions, `plugins/`, `hooks/`,
`projects/`, `output-styles/`, history, and everything else in the
config root.

### Symbolic links

(decided on I45 on 2026-10-12, SEC02-no-host-fs-share).
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
  as a link and never reads its target (S08-01-into-the-guest, "Carry-in scan"), because the guest gets the same repository and
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
  (S14-03-configuration-filter, "Plugin and hook files").

**Status:** Draft
