# X20 - Homebrew with more than one project user

**Purpose:** Whether each project user in one macOS guest can get its project's Homebrew packages from a prefix that no other project user can write to, and what a prefix per project user would cost instead.

Brief: B31-shared-homebrew

## For review

- **Decides:** the answer to X20-shared-homebrew (I31), and how
  S06-vm-lifecycle, "Images", now describes the toolchain prefix and
  the project layer. It confirms the I144 decision (the shared prefix
  belongs to a toolchain user) for how projects add packages.
- **You are approving:** the answer "yes, with conditions", and the
  spec text in this pull request. One prefix, `/opt/homebrew`, belongs
  to the toolchain user. `wb-guestd` installs every project's declared
  packages into it as that user. Project users can read and run it
  but can't install packages, and the agent runs as a project user.
  The strongest of the conditions is that the manifest is read as a
  list of package names. A Brewfile is Ruby: run with
  `brew bundle`, it ran a command as the toolchain user and wrote into
  the prefix that every project runs from.
- **Controls touched:** none weakened. SEC08-proj-isolation gains the
  manifest rule: without it, any project's Brewfile could plant a
  binary that other projects run. Projects in one VM share the
  versions in the prefix, much as they share network grants
  (T11-shared-vm-grants). The spike didn't measure whether a project
  can change what another project's session runs while it runs
  (condition 4).
- **Assumed:** that the conditions the spike didn't try work as written:
  Homebrew's temporary and cache folders in the toolchain user's home,
  `HOMEBREW_NO_INSTALL_UPGRADE` keeping a reconcile from changing kegs
  other projects use, and a session `PATH` without the prefix's shared
  `bin`. The spike used a NAT network, not the egress gateway, and the
  toolchain user name `_wbtool` is a stand-in until I144 picks one.
- **Open decisions:**
  1. Whether a project may get a prefix of its own instead, where the
     agent can run `brew install`. It works when the path is at most
     13 characters long, such as `/opt/wbp-c`, and every bottle then
     pours. Recommended: not in v1. The shared prefix covers declared
     packages, `--isolated` covers conflicts, and an agent that
     installs packages is a capability v1 doesn't need.
- **Brief:** B31-shared-homebrew

## Question

From X00-index: can each project user in one guest get its declared
Brewfile without a Homebrew prefix that another project user can
write to (SEC08-proj-isolation)? Compare a prefix owned by a dedicated
user that `wb-guestd` drives with per-user prefixes. Measure bottle
availability and install time for both. I31 adds: check version
conflicts between projects, what a project user can and can't change,
and whether the agent may run `brew install` itself. In the I144
decision, the shared prefix belongs to a toolchain user that isn't an
admin and has neither a password nor a shell. Only `wb-guestd` uses
it, through `sudo -u`, and project users may only read and execute
the prefix.

## Answer

**Yes, with conditions.** The shared prefix owned by the toolchain user
works, and a project user can't change it. A prefix in each project
user's home doesn't work in practice: Homebrew builds about a sixth of
a typical Brewfile's formulae from source there, and with LLVM among
them the install was still running after 51 minutes. A per-user prefix
at a path no longer than `/opt/homebrew` pours every bottle.

1. **The toolchain user can own and run Homebrew: yes.** Root created
   `_wbtool` (uid 440, its own group, no shell, password `*`, hidden,
   in no admin group) and `/opt/homebrew` owned by it. Homebrew's own
   installer, run as `_wbtool` through `sudo -u` with a clean
   environment and `NONINTERACTIVE=1`, installed Homebrew 7.0.8 without
   asking for `sudo`, because the prefix already existed and belonged
   to it. Every file in the prefix belongs to `_wbtool` and its group,
   none is writable by others, and none is set-user-ID.
2. **A project user can't change the shared prefix: verified.** As a
   standard project user, every write was refused: replacing the
   `bin/git` link, a new file in `bin`, appending to the `git` binary
   in `Cellar`, a new directory in `Cellar`, editing Homebrew's Ruby,
   writing to `etc`, `var`, `lib/node_modules` and Python's
   `site-packages`, and `chmod` of `bin`. `brew install tree`,
   `brew uninstall`, `brew link` and `brew update` failed on
   permissions. `sudo -n -u _wbtool` asked for a password, `su` failed,
   and the toolchain user's home was unreadable.
3. **The agent can't install packages there, and installs into its home
   instead: verified.** `brew install` and `npm install -g` fail for a
   project user. `pip install` falls back to the user's own
   `site-packages` (and refuses without `--break-system-packages`, as
   PEP 668 says), `npm` with `NPM_CONFIG_PREFIX` in the home works, and
   so do `uv tool install`, `go install`, and `initdb` of a PostgreSQL
   data directory in the home.
4. **A Brewfile runs code as the toolchain user: yes.** A Brewfile with
   one `system` line and one `brew "jq"` line, run with
   `brew bundle install`, created `/opt/homebrew/bin/x20-from-brewfile`,
   owned by `_wbtool`. So a project that writes its own Brewfile can
   plant a file in every project's `PATH`. Homebrew 7 refused a formula
   file passed by path, pointed to `brew tap-new`, and the code in the
   file didn't run. A tap's
   formulae are Ruby too, so a third-party tap would also run as the
   toolchain user. `brew install --formula jq tree`, with names only,
   poured `tree` and ran nothing else.
5. **Version conflicts: versioned formulae live side by side, and real
   conflicts are refused.** Project B's `node@22`, `python@3.12`,
   `postgresql@16` and `go@1.25` installed next to project A's `node`,
   `python@3.13`, `postgresql@17` and `go`, unlinked or keg-only, and
   each ran at its own version from `opt/<formula>/bin` (and
   `python@3.12`'s `libexec/bin` for `python3`). Which version an
   unversioned name runs depends on every project, though: `python3`
   in the shared `bin` was 3.14, which project A pulled in as a
   dependency of `awscli`, although A declared `python@3.13`. Project
   C's `mysql` was refused, because B's `mariadb` installs the same
   binaries ("Cannot install mysql because conflicting formulae are
   installed"), and `brew bundle` exited 1.
6. **A prefix in the project user's home: no.** In `~/homebrew`
   (`/Users/wbp-b/homebrew`, 21 characters), Homebrew 7 builds a
   formula from source when its bottle names `/opt/homebrew` and the
   prefix is longer than 13 characters. Of project A's 59 formulae,
   9 have such bottles (`gettext`, `git`, `krb5`, `node`, `openssl@3`,
   `pkgconf`, `postgresql@17`, `python@3.13`, `python@3.14`). `node`
   needs `rust` to build, and `rust` needs `llvm`, whose bottle is the
   same kind, so LLVM is built too. Homebrew also printed that this
   prefix is "not a Tier 1 configuration" and not to report issues.
   After 51 minutes the spike stopped the build, with 5 formulae built
   and LLVM at step 2963 of 9131.
7. **A per-user prefix at a short path: yes.** In `/opt/wbp-c`
   (10 characters), created by root and given to the project user,
   all 59 formulae poured from bottles in 230 s, and none was built.
   The relocated tools worked: `git` cloned over HTTPS, Python built
   a C extension from source in a virtual environment against its relocated headers,
   and `psql` and `node` ran. Homebrew failed to sign 8 static
   archives (`.a` files) and carried on, and 15 files outside the
   install receipts still name `/opt/homebrew` (documentation, and
   `python@3.14`'s build settings, `uvwasi.pc` and `pcre2-config`). A
   symbolic link `/opt/wbp-d` to a directory in the home doesn't help:
   Homebrew followed it, saw a 16-character prefix, and started
   building from source.

The conditions of the shared prefix (S06-vm-lifecycle, "Toolchain
prefix"):

1. **The manifest is data.** `wb-hostd` reads it as a list of formula
   names, and `wb-guestd` installs them with
   `brew install --formula <names>` as the toolchain user. Neither
   runs `brew bundle` or hands Homebrew a file a project wrote. A
   manifest line that isn't `brew "<name>"` with a plain formula name
   (a tap, a cask, options, `system` or any other Ruby) is refused,
   with the line and the rule logged (answer 4).
2. **Formulae come from `homebrew/core` only**, because a third-party
   tap is code that would run as the toolchain user (answer 4).
3. **Only `wb-guestd` installs.** Project users and the agent have read
   and execute only, no `sudo`, and no way to the toolchain user
   (answers 2 and 3).
4. **A reconcile only adds.** `HOMEBREW_NO_AUTO_UPDATE`,
   `HOMEBREW_NO_INSTALL_UPGRADE` and `HOMEBREW_NO_INSTALL_CLEANUP` are
   set, so installing one project's packages doesn't upgrade or remove
   kegs that another project's session runs. The spike didn't try
   this.
5. **The session's `PATH` comes from what it declared**, not from what
   the prefix happens to link: `opt/<formula>/bin` for each formula of
   the image's recipe and the project's manifest (and Python's
   `libexec/bin`), without the prefix's shared `bin` (answer 5). The
   spike didn't try a `PATH` without the shared `bin`.
6. **A conflict fails the reconcile, and `wb` refuses the session**
   with both formula names and `--isolated` (answer 5).
7. **The toolchain user's environment is built by `wb-guestd`** from an
   allowlist, with `HOMEBREW_TEMP` and `HOMEBREW_CACHE` in its own
   home, which is mode 700. The default temporary folder is the shared
   `/private/tmp`. The spike didn't try this.

## Measurements

Host: Apple M2, 8 cores, 16 GB, macOS 27.0.1 (26A434), no `sudo`. Guest:
a fresh APFS clone of the X17-image-build spike's sealed bundle
(macOS 27.0.1, 4 vCPU, 4 GiB, 64 GiB sparse disk), on Apple's NAT
network, with the Xcode command line tools installed first as I150
puts them in the base image. Homebrew 7.0.8. Bottle tag
`arm64_golden_gate`. Project A is a realistic polyglot Brewfile:
`git`, `gh`, `jq`, `ripgrep`, `fd`, `node`, `python@3.13`, `uv`, `go`,
`mise`, `cmake`, `pkgconf`, `postgresql@17`, `redis` and `awscli`,
59 formulae with their dependencies. Each run downloaded every bottle
afresh into its own cache.

| Step | Time | Disk |
|---|---|---|
| Xcode command line tools from Apple, as root | 189 s | 1.3 GB |
| Homebrew installer as the toolchain user | 35.4 s | |
| Shared prefix, project A (59 formulae, all poured) | 179 s | 1.4 GB, cache 436 MB |
| Shared prefix, project B after A (5 formulae, 13 new kegs) | 66 s | 2.2 GB with A |
| Shared prefix, project C (`mysql`, conflicts) | refused in 13 s | |
| Shared prefix, project A again, nothing to install | 0.42 to 0.83 s | |
| Home prefix, project A | stopped at 51 min, unfinished | 2.1 GB so far |
| Short prefix `/opt/wbp-c`, project A (all poured) | 230 s | 1.7 GB, cache 438 MB |

- **Source builds in the home prefix**, from Homebrew's log folders in
  the guest: `pkgconf` 13 s, `git` 44 s, `openssl@3` 3 min 45 s,
  `python@3.14` 5 min 26 s, `gettext` about 7 min including its
  downloads, and LLVM 31 min to step 2963 of 9131. At that rate LLVM
  alone takes about 1 h 35 min, before `rust`, `node`, `python@3.13`,
  `krb5` and `postgresql@17`.
- **Bottles by kind**, from Homebrew's formula API for project A's 59
  formulae: 43 relocatable (`:any`), 7 that pour anywhere unchanged
  (`:any_skip_relocation`), and 9 built for `/opt/homebrew` only.
- **Prefix size:** the shared prefix holds 2.9 GB after projects A
  and B and `tree`, all owned by the toolchain user.

## What was not measured

- **The egress gateway.** The guest had a NAT network. Bottles come from
  `ghcr.io`, which X22-no-guest-credentials and S07-egress-gateway
  already cover.
- **A reconcile while another project's session runs** (condition 4),
  and whether the three variables keep Homebrew from changing shared
  dependencies when the formula data is newer than the installed kegs.
- **A session `PATH` without the prefix's shared `bin`** (condition 5).
  Tools that look up a dependency's command by name may need it.
- **Homebrew's temporary and cache folders in the toolchain user's
  home** (condition 7).
- **The full source build in the home prefix.** The spike stopped it to
  keep the host's disk free.
- **Derived images.** I150 builds images from recipes as the toolchain
  user. The spike installed into a running guest.

## What it means for the specs

- **S06-vm-lifecycle**, "Images": a new "Toolchain prefix" item with
  the conditions, and the project layer installed by `wb-guestd` as
  the toolchain user (changed in this pull request). The base image,
  the org layer, and sealing stay as they are, because I150 and I144
  rewrite them. The sealing scan has to accept the toolchain user and
  its prefix: I144 decided the scan checks kept users by name, and
  I150 lists the toolchain user's areas.
- **S11-verification-and-spikes**: a conformance check that a project
  user can't write the prefix or install, and that a manifest with
  anything but formula names is refused, and X20-shared-homebrew in the
  list of answered spikes (changed in this pull request).
- **FR06-native-tools and FR07-toolchain-manifest** still say Homebrew
  and Brewfile. I150 rewords them to the guest's package manager. The
  manifest keeps the Brewfile's `brew "<name>"` lines, so a simple
  Brewfile is still a valid manifest.

## Spike code

Branch `spike/x20-shared-homebrew`, at
[bff92f2](https://github.com/wraithbox/wraithbox/tree/bff92f2f43c139a187316bc678286aaa1a2d3ca5/spikes/x20-shared-homebrew):
the X17 `wb-vmd` stand-in with a relay mode, the request client, the
bottle-kind script over Homebrew's formula API, a Brewfile for each test project, the
guest scripts, and the raw results in `results/`. The spike's bundle is
a spike artifact, and no product image may descend from it.

**Status:** Answered 2026-10-07: yes, with conditions
