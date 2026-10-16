# S17 - Install, Downloads and Upgrades

**Purpose:** Build, install, upgrade and remove a Wraith Box bundle,
move its stored data to a new version, and download what an image
build needs from the internet.

**Requirements:** SEC12-least-privilege (no root, each process gets only
what it needs), SEC10-audit (every egress decision recorded),
SEC13-bounded-resources (what the host spends is capped),
NFR06-explained-refusals (each refusal names its rule and the fix).

**Tests:** C12-install-and-downloads

Brief: B49-install-upgrade

## Design

### Pages

| Page | What it covers |
|---|---|
| S17-01-bundle | The bundle's layout, and how `mise run build` builds and signs it |
| S17-02-setup | `wb setup`: the bundle check, the LaunchAgent, the `wb` link, and removal |
| S17-03-downloads | `wb-download`: entries, confinement, host rules, deadlines, records, audit and space |
| S17-04-restore-and-release | The steps that fetch a restore image and a Claude Code release |
| S17-05-upgrades-and-migrations | Replacing the bundle, and migrating `state.db` and configuration |

## Out of scope

- Release builds: Developer ID signing, notarization and the Homebrew
  cask (S10-tech-stack).
- A restore image from a file the user names, for hosts without access
  to Apple's servers (I263).
- A macOS version other than the newest the host supports, apart from
  `--allow-older-macos`, and an upstream proxy for downloads.
- Deleting data and store items at uninstall (I264), and downgrades:
  an older `wb-hostd` refuses a newer `state.db`.

**Status:** Draft
