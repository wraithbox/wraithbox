# S16 - Terminal Stream

**Purpose:** How `wb` guards boundary 6 of T00-index, the
guest output that `wb` writes to the user's terminal.

**Requirements:** FR01-drop-in, SEC03-no-host-exec, SEC10-audit, SEC14-no-fake-approvals, NFR06-explained-refusals.

**Tests:** C10-terminal

Brief: B37-terminal-boundary

## Design

The terminal emulator parses the guest's output, and acts on some of
it on the host:

- OSC 52 writes the clipboard;
- iTerm2's OSC 1337 saves files;
- kitty graphics can read a file by its path.

Every parser bug is a path to the host. So guest output is boundary 6
of T00-index. `wb` filters it with an allowlist that fails closed, as
`wb-netd` filters packets (SEC03-no-host-exec).

### Pages

| Page | What it covers |
|---|---|
| S16-01-filter | Where the filter runs, how it splits the stream, and the allowlist |
| S16-02-drops-and-links | The drop rules the counts use, and the links the filter drops |
| S16-03-around-the-stream | What `wb` writes around the stream, the environment, terminfo, and guest text in other output |

## Out of scope

- The TTY relay itself, and the exit status and signals of `wb claude`,
  are in S05-cli ("Agent commands", "Exit status and signals").

**Status:** Draft
