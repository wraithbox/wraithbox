# S14 - Claude Code in the Guest

**Purpose:** How Claude Code gets into the guest image, which of the
user's Claude Code configuration goes with it, how it starts, and the
agent interface that keeps Claude Code's specifics in one place
(V1-02-claude-only).

**Requirements:** FR01-drop-in, FR13-claude-state, SEC02-no-host-fs-share, SEC03-no-host-exec, SEC04-no-guest-secrets, SEC05-default-deny, SEC07-dep-gate, SEC10-audit, SEC12-least-privilege.

**Tests:** C06-model-api

Brief: B45-claude-in-guest

## Design

![Claude Code in the guest. wb reads an allowlist of the user's ~/.claude/, filters and scans it, and wb-hostd hands the copy to wb-guestd, which writes it as the project user. ~/.claude.json never reaches the guest. wb-download fetches a Claude Code build on the host, wb-release-check checks it for wb-hostd, and wb-guestd installs it root-owned during the image build. Claude Code reaches Anthropic only through wb-proxyd, which injects the model credential.](S14-00-claude-in-guest.svg)

### Pages

| Page | What it covers |
|---|---|
| S14-01-install | How the image build downloads, checks and installs Claude Code, and its version |
| S14-02-user-configuration | Which of the user's `~/.claude/` entries `wb` reads, and how it treats symbolic links |
| S14-03-configuration-filter | The filter, scan and caps the copy passes, and how `wb-guestd` writes it |
| S14-04-commands-and-endpoints | The permission mode, the commands that don't work in a guest, and the endpoints Claude Code may reach |

### Agent interface

Claude Code is the only agent in V1, and the interface is what Claude
Code needs, with no general design before a second agent exists
(decided on I45, row 6). Agent-specific code is in one Go package per
agent, `internal/agent/claude`, and `wb`, `wb-hostd` and `wb-guestd`
call it only through the interface in `internal/agent`. An agent supplies:

- **Command.** The agent command name for `wb` (`claude`, S05-cli,
  "Agent commands") and the program `wb-guestd` starts.
- **Install.** How the image build gets and checks the agent's build
  (S14-01-install).
- **Configuration carry-in.** Which of the user's files reach the
  guest, and the filter they pass (S14-02-user-configuration).
- **Launch.** The environment variables `wb-guestd` adds to the
  session's allowlist, and the state the agent keeps per project
  (S06-08-projects-and-sessions, "Claude Code in the guest").
- **Credential bindings.** The kinds of model credential, the guest
  variable that holds each placeholder, and the header each binding
  injects (S09-04-credentials, "Model credential").
- **Endpoint profile.** The built-in HTTP profile the agent needs, the
  model API profile for Claude Code (S07-07-http-policy).

## Out of scope

- Carrying in a hook or an MCP server the user names. V1 carries none.
- Plugins, and `keybindings.json`, output styles, and status line
  scripts in `~/.claude/`.
- A project's own `.mcp.json` and `.claude/` in the repository. They
  reach the guest as part of the work, and Claude Code treats them as
  project configuration.
- Agents other than Claude Code (V1-02-claude-only).

**Status:** Draft
