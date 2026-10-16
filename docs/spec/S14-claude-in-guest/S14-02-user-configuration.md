# S14-02 - User Configuration

**Purpose:** Which of the user's Claude Code configuration `wb` reads
at session start, and how it treats symbolic links in it.

## Design

At each session start, `wb` filters and scans an allowlist of the
user's `~/.claude/`. It copies the result into the project user's
home.

`wb-hostd` can read only its state directories
(S04-03-self-sandboxing, "Each host daemon is self-sandboxed"). So
`wb`, which runs as the user, reads the files.

The files can come from a repository the user merged the guest's work
into. So `wb` treats them as untrusted input. Its settings and front
matter parsers have fuzz targets.

### Config root

The config root is `~/.claude/`, or `CLAUDE_CONFIG_DIR` when the
user's environment sets it.

### Entries

`wb` reads these entries of the config root:

- `CLAUDE.md` and `settings.json`;
- the directories `agents/`, `skills/` and `commands/`.

It never reads:

- `.claude.json`, in the home or in `CLAUDE_CONFIG_DIR`. It holds the
  sign-in session and the MCP server definitions.
- `plugins/`, `hooks/`, `projects/` and `output-styles/`;
- history, and everything else in the config root.

### Symbolic links

These rules serve SEC02-no-host-fs-share.

- *Each entry once.* `wb` resolves each entry's path once, following
  links, to get that entry's root. So a `skills` directory linked into
  a repository works, as it does for Claude Code on the host.
- *Inside a root.* A link whose target resolves outside its root is
  skipped with the rule `claude-config-link-outside`. `wb` doesn't
  read the target.
- *Never `.claude.json`.* A root or a link that resolves to
  `.claude.json` in the home or in `CLAUDE_CONFIG_DIR` is skipped
  unread, with the rule `claude-config-link-claude-json`.
- *Other file types.* Anything other than a regular file, a directory,
  or a link is skipped. A link loop is skipped.
- *Hidden entries.* Inside a root, `wb` skips each entry whose name
  starts with `.`, such as `.git` or `.env`, and doesn't read it. The
  one exception is `.claude-plugin/`, which skips its whole unit
  (S14-03-configuration-filter, "Plugin and hook files").

#### Roots `wb` refuses

A root is skipped unread, with the rule `claude-config-root-refused`,
when its resolved path:

- is the home directory or one of its ancestors;
- has a component outside the config root that starts with `.`, such
  as `~/.ssh` or `~/.config/gh`;
- lies under the platform's user data directory: `~/Library` on macOS,
  `%APPDATA%` and `%LOCALAPPDATA%` on Windows.

Those hold credentials and application data, not Claude Code
configuration.

#### Why this differs from S08

The working tree carry-in sends a link as a link, and never reads its
target (S08-01-into-the-guest, "Carry-in scan"). The guest gets the
same repository there, and needs only the link.

A configuration entry is useless as a dangling link in the guest. And
users link these entries on purpose. So `wb` follows the one link the
user made at the top, and no link below it that leaves the root.

#### Residual

Inside a root, a link to another file of the same root is followed.

The root can be in a repository the user merged the guest's work into.
A link the agent committed there can name a file of that repository
the guest never got, such as an ignored `.env`. Only the secret scan
stands between that file and the guest (T20-config-root-links).

## Decisions

### Copy a filtered allowlist of `~/.claude/`

`wb` copies a filtered and scanned allowlist of the user's
`~/.claude/` at each session start.

- **Rejected:** an empty configuration per project. It makes
  `wb claude` unlike `claude` (FR01-drop-in).
- **Rejected:** a copy of `~/.claude/` and `~/.claude.json` as they
  are. It puts the sign-in session and MCP server tokens in the guest
  (SEC04-no-guest-secrets).
- **Decided:** I45, row 3, option A.

### The rules for symbolic links

`wb` follows the link the user made at the top of an entry, and no
link below it that leaves the root.

- **Decided:** I45, on 2026-10-12.

**Status:** Draft
