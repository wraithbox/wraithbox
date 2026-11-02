# F: the `wb claude` user journey, step by step

Agent F, X32-project-metrics wave 1. Main at `6138336c`, 2026-11-02.
"Impl." is what the code on main does today. "Tested" is the strongest
layer that exercises the step:

- **unit**: Go/Swift tests in CI (Linux, macOS, Windows);
- **VM by hand**: a `needs-vm` test (`mise run go:test-vm`, `TestC09ShellVM`,
  `TestC11HostdVM`) that the maintainer or an agent ran on the maintainer's
  Mac. CI never boots a VM: `go:test-vm` needs `WB_VM_TEST_BUNDLE` and
  isn't in `.github/workflows/ci.yml`;
- **attended**: the maintainer at the keyboard, recorded in an issue;
- **spike**: throwaway code, measured once, not product code.

Milestones: M3 network floor, M4 `wb claude` end to end, M5 approvals,
M6 release gate, M7 image build.

## 1. Install and first-time setup

| # | Step as the user lives it | Spec | In V1 | Impl. today (evidence) | Tested |
|---|---|---|---|---|---|
| 1.1 | Has Apple Silicon and macOS 27 (needed by `wb image build`) | S12-03, NFR04 | yes | yes: `internal/setup/host.go` refuses `setup-host-unsupported` | unit |
| 1.2 | Signs in to Xcode with an Apple ID, builds a small app once in the Xcode app to register the Mac with a personal team, notes the team ID | S12-03 "Development builds on macOS", X29 | yes. Only dev builds in V1. Developer ID, notarization and a Homebrew cask are out of scope (S17-00, S10-02) | yes, the build side: `mise run build` with `WB_TEAM_ID` (#280) | attended: H1 on the maintainer's Mac (#276). X29 measured a 365-day profile on a lapsed paid team. A free team is unverified and may get 7 days (X29 "Limits") |
| 1.3 | Clones the repo, `mise install`, `mise run build` (Go plus Swift, signed), moves `Wraith Box.app` to `~/Applications` | S17-01 | yes | yes (`app:bundle` task, `scripts/app-bundle.sh`) | CI builds the Swift package ad hoc. The signed bundle is built by hand |
| 1.4 | `wb setup`: checks host and git, makes `<data>/projects`, writes the LaunchAgent, links `~/.local/bin/wb`, reports the signing team | S05-03 "wb setup", S17-02 | yes | yes: `internal/setup` (`setup.go`, `agent.go`, `link.go`, `keychain.go`). Profile-expiry warnings not built (M6.6) | unit and darwin tests in CI (C05, C12 cases). Run on the maintainer's Mac for H1 |
| 1.5 | Runs `claude setup-token` on the host (browser login there), stores the 1-year token with `wb cred set` | S09-05 "Model credential", S15-02, X01 | yes (M4.10, #504) | no: `wb cred: not implemented yet`. `wb-credset` is a stub. The secret store interface and a fake are in (#503). The Keychain shim is open (#505, #509) | spike X01: 36 of 36 requests injected, `claude -p` 1.8 s. API key mode never run (#253) |
| 1.6 | `wb image build`: downloads a 26.6 GB restore image, installs macOS, provisions over one SSH session, installs CLT, Homebrew and Claude Code layers, seals. Optional Xcode variant | S06-01, S06-02, S14-01, S17-03, S17-04, X17, X06 | yes (M7) | no: `wb image: not implemented yet`. 4 of 13 M7 issues closed, all spec decisions. Test VMs are made with dev tools (`wb-dev-vm`, `mise vm:*`, #350) | spike X17: 6.6 min from a downloaded restore image to a sealed bare image. Download time unmeasured (I49). Base layer, Claude layer and Xcode variant never timed together |

## 2. First `wb claude` in a project

| # | Step | Spec | In V1 | Impl. today | Tested |
|---|---|---|---|---|---|
| 2.1 | `cd repo; wb claude`. `wb` refuses outside git and on a non-UTF-8 locale | S05-01 | yes | the parser accepts `claude`, then `cmd/wb/main.go` prints `wb claude: not implemented yet`, exit 2. No `internal/agent` package. The UTF-8 refusal exists for `wb shell` (#377) | unit (parser) |
| 2.2 | First run registers the project: id, `export.git`, guest user, home on the data disk | S05-04, S06-08, S08-02 | yes | partly: `internal/project`, `internal/projectcmd` register on `wb project place` and `wb shell`. Guest users and homes (#296, #297, #290) | C04 tests in CI (darwin). Users and homes: VM by hand (#296, #304) |
| 2.3 | Host repo exported and cloned into the guest over vsock | S08-01, S08-02, X07 | yes (M4.1, M4.2) | no | spike X07: 426 MiB repo, export 16.2 s plus first clone 16.6 s. Later fetches 0.6 s |
| 2.4 | Uncommitted changes carried in, after a secret scan | FR04, S08-01 | yes (M4.8) | no. The secret scanner is undecided (#306, open in M7) | none |
| 2.5 | Allowlisted `~/.claude/` (CLAUDE.md, settings.json, agents, skills, commands) copied in. Never `.claude.json` (no MCP servers), plugins, hooks | S14-02, S14-03 | yes (M4.9) | no | none |
| 2.6 | Toolchain reconcile from a host-side Brewfile (homebrew/core names only, bottles only) | FR07, S06-01 | yes (M5.11) | no | spike X20. Untested conditions are #180 (open, needs-vm) |
| 2.7 | VM cold boots (no warm start yet), `claude` starts in a new worktree with the model placeholder | S06-06, S06-09, S14 | yes | cold boot only: `wb vm start` (#288). No session start, no worktree, no placeholder | VM by hand: start to hello 14.1 s and 19.4 s (C11 runs, 2026-10-25). Never measured to the Claude prompt |
| 2.8 | Warning at first session never to paste a login code into the guest | S14-04 "Warning" | yes | no: `wb help` doesn't say it yet | none |

## 3. Daily use

| # | Step | Spec | In V1 | Impl. today | Tested |
|---|---|---|---|---|---|
| 3.1 | Start time of each session: restore of a suspended VM, or warm VM | NFR01, S06-06 | warm start and suspend in M6.4 | no: `wb vm suspend` is refused with `vm-suspend-needs-warm-start` | spikes X02 (restore 3.7/3.9 s to network) and X18 (4.1/4.2 s to vsock). Not to the prompt |
| 3.2 | Files: work is on the guest data disk, one new worktree per session. Host editor can't see it until the work returns | S06-05, S06-08, SEC02 | yes | data disk: yes (`wb-diskfmt` #352, mount #416). Worktrees: no | VM by hand (#290, #304), X05, X08 |
| 3.3 | Tools: `npm`, `pip`, `uv`, `go` install into the home. `brew install` refused for project users. Xcode builds and iOS simulators work. macOS `xcodebuild test` and launching macOS apps don't | FR06, S06-01, S06-04, X06, X20 | yes (M7 IMG.6, IMG.8) | no. How Xcode gets into the image is still open (#194) | spikes X06, X20 |
| 3.4 | Network: default deny. Built-in profiles allow git hosting reads, the model API and package registries. Other names held 4 s at DNS and offered for approval | S07, S09-10, FR08, FR09 | yes (M3, M5) | partly: packet path (#482), VM network device (#481), audit writer (#476), event channel (#477). `wb-netd` and `wb-proxyd` binaries print `not implemented yet`. DNS, policy, TLS, HTTP and CA install open (#483 to #513) | unit and fuzz in CI. VM conformance (#513) open |
| 3.5 | Approvals as native notifications, or `wb approve <id>` from another terminal. Learn mode to collect a project's hosts | FR09, FR10, S07-17, S09-09, S09-10 | yes (M5) | no: `wb approve: not implemented yet`. The notification helper contract isn't drafted (M5.4, needs H5 attended permission) | none |
| 3.6 | Dependency gate: young versions (under 7 days) hidden and refused, CRITICAL CVEs and MAL reports refused | SEC07, S07-12 | yes (M5.7, M5.8) | no (`wb gate` is not even a known command) | spike X21 on 10 real projects |
| 3.7 | Agent commits and pushes to `landing.git`. User runs `wb diff`, `wb land`, then merges and pushes from the host. The guest has no forge credential and `gh pr create` (GraphQL mutation) is refused | FR03, S08, S07-11, #502 | yes (M4) | no: diff, land, discard, sessions print `not implemented yet` | spikes X07, X26, X28 |
| 3.8 | Session summary on exit: returned commits, flagged paths, terminal drops, links | S05-01, S16 | yes (M4.11) | the terminal filter is in (M2, `internal/termfilter`). The summary isn't | VM by hand: attended terminal checks on #471 showed drop counts (C10) |

## 4. Stop and resume

| # | Step | Spec | In V1 | Impl. today | Tested |
|---|---|---|---|---|---|
| 4.1 | Closing the terminal ends the session, WIP committed and pushed. No detach | S05-01, S06-09 | yes (M4.6, M4.7) | no | none |
| 4.2 | `wb claude --continue` starts a new session that resumes the last conversation through a per-project history link | FR13, S06-08 | yes (M4.12) | no. The link layout is unchecked against Claude Code in a guest (#135, open) | none |
| 4.3 | Laptop sleeps mid-session: session pauses and resumes. 30 s awake without the link loses it | S06-09 "Host sleep", S06-11 | yes | link reconnect: yes (`internal/guestlink`, reconnect in 103 ms in C11). Session pause: no | VM by hand (C11) |
| 4.4 | `wb vm stop` / `start` explicitly | S05-03 "wb vm" | yes | yes. Every stop waits the full 30 s timeout because the guest ignores the requested stop (#414, open) | VM by hand (C11) |
| 4.5 | Idle VM suspends and restores in about 4 s | NFR03, S06-06 | M6.4 | no | spike X02 |

## 5. Upgrade

| # | Step | Spec | In V1 | Impl. today | Tested |
|---|---|---|---|---|---|
| 5.1 | New Wraith Box build: rebuild, replace bundle, `wb setup` (refused while VMs run), `state.db` migrated | S17-05 | yes (M6.6) | partly: `setup-vms-running` refusal exists. Migrations and bundle-replaced detection not built (#438, #332) | unit |
| 5.2 | New Claude Code: `wb image build` rebuilds only the Claude layer from the stable channel (about a week behind `latest`), then the VM's system disk is swapped | S14-01, S06-03 | yes (M7 IMG.7) | no | none |
| 5.3 | New guest macOS: rebuild the base image. The restore image is deleted after each sealed build, so this downloads 26.6 GB again | S06-02, S17-04 | yes | no | spike X17 |
| 5.4 | Signing profile expires (1 year, possibly 7 days on a free team): warnings in `wb setup`, `wb status`, `wb claude`. Rebuild the Xcode app and Wraith Box | S12-03 | yes (M6.6) | no | none |

## 6. Recovery when something breaks

| # | Step | Spec | In V1 | Impl. today | Tested |
|---|---|---|---|---|---|
| 6.1 | `wb status` to see VMs, sessions, approvals, daemon health | S05-03 | yes (M4.6) | no: `wb status: not implemented yet`. `wb vm status` exists | unit (vm status) |
| 6.2 | Lost session recovered at next connection, or `wb discard` | S06-11, S08-07 | yes (M4.7, M4.5) | no | none |
| 6.3 | Data disk refused or broken: `wb vm reset-data` | S05-03 | yes | yes: `internal/vmcmd/reset.go` with confirmation, #352 | unit |
| 6.4 | CA restore refused: `wb vm reset-ca` | S05-03 | yes (M3, #507) | no: not in the `wb vm` usage | none |
| 6.5 | Root shell for the person at the keyboard: `wb vm admin` | S15-05 | yes | no. Its channel is still a spike (X31, #448) | none |
| 6.6 | Refusals name the rule and the fix: `wraith box: <message> (<rule>)` | NFR06 | yes (M5) | yes for the commands that exist (vm, project, shell, setup) | unit |
| 6.7 | Uninstall: `wb setup --remove` | S17-02 | yes (M6.6) | no: prints `--remove is not built yet` | unit |

## The CLI today against S05

Built with `go build ./cmd/wb` from main. Output in `cli-run.txt`, script in
`spikes/x32-project-metrics/f-fitness/cli-probe.sh`. I didn't run anything
that starts a VM, writes host state, or reaches the network. `wb setup`,
`wb claude`, `wb vm start/stop/reset-data` and the others were read in code, not run.

- `wb`, `wb help`, `wb --help`: the S05 command list, exit 0. Matches S05.
- `wb version`: `wb (Wraith Box) unstamped`. `mise run build` stamps it.
- **Built:** `setup`, `vm start|stop|status|reset-data`, `vm suspend` (a
  refusal by design until warm start), `project show|confirm|move|rename|place|rm`,
  `shell`, `version`, `help`. That is 6 of the 22 commands in `wb help`.
- **Not built, exit 2 with `wb <cmd>: not implemented yet`:** `claude`, `status`,
  `sessions`, `diff`, `land`, `discard`, `approve`, `deny`, `allow`, `policy`,
  `learn`, `trust`, `untrust`, `cred`, `audit`, `image`. That is 16 of 22.
- **Drift from S05-03:** `wb gate reset go-clock` is in the spec, but
  `wb gate` is an unknown command and isn't in the help. `wb vm reset-ca` and
  `wb vm admin` aren't in the `wb vm` usage. `wb setup --remove` says "not built yet".
  The help lacks the login-code warning S14-04 asks for.
- **Small usability points:** `wb vm --help`, `wb project --help` and
  `wb shell --help` are usage errors: there's no help per command, and S05 doesn't
  ask for one. "not implemented yet" exits 2, the usage error status, though
  it isn't a usage error. `wb --bogus` reports `-bogus` with one dash (Go's
  `flag` text).
- The refusal for session flags before a non-agent command is clear:
  `--isolated, --ephemeral, --learn and --guest only apply to agent commands such as claude`.
