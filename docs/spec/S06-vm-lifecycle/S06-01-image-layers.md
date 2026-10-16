# S06-01 - Image Layers and the Toolchain Prefix

**Purpose:** The layers of a guest image, and the toolchain prefix that
project layers install into.

## Design

### Layers

A guest image has three layers:

1. *Base*: macOS installed from an Apple restore image, plus
   `wb-guestd`, Homebrew, the Xcode command line tools, and Claude Code
   (S14-01-install). An Xcode variant adds full Xcode.
2. *Org layer*, optional: a shared Brewfile and settings.
3. *Project layer*: the formulae the project declares
   (FR07-toolchain-manifest). `wb-guestd` installs them as the
   toolchain user into the toolchain prefix, before the session starts
   ("Toolchain prefix" below).

The image doesn't contain a Wraith Box CA. `wb-guestd` installs the
VM's CAs at runtime in the guest trust files. On Linux and Windows
guests it also installs them in the system store (S09-08-guest-trust).

### Toolchain prefix

A guest has one tool prefix, `/opt/homebrew` on macOS
(X20-shared-homebrew). The toolchain user owns it (I144).

That user:

- isn't an admin;
- has neither a password nor a shell;
- has a home of mode 700.

Only `wb-guestd` runs commands as the toolchain user. It does that in
one of two ways:

- through `sudo -u` from root (I144);
- by setting the child's user and group IDs itself
  (`syscall.Credential` in Go), with the toolchain group as the only
  supplementary group. The implementation may prefer this, because
  then `wb-guestd` alone sets the environment.

Project users, the agent included, can read and run the prefix. They
can't write to it or install packages. They install language packages
into their own home instead.

Who may write the prefix, and that a manifest is names only, keep one
project from changing the tools of another (SEC08-proj-isolation). The session `PATH` rule below keeps sessions
reproducible, and isn't a control. Projects still share the prefix's
contents (T15-shared-toolchain).

#### Who may write

Every file in the prefix belongs to the toolchain user and to its own
group. That group has no other members. Others have no write
permission on any of the files.

#### The manifest is data

A Brewfile is Ruby, and `brew bundle` runs it as the toolchain user. A
project could then plant a file in the prefix that every other project
runs from. So `wb-hostd` reads the manifest as a list of formula names.

- Each line is `brew "<name>"`, a comment, or blank.
- Each name matches `^[a-z0-9][a-z0-9+._@-]*$`, doesn't end in `.rb`,
  and is at most 64 characters long.
- A manifest has at most 1000 lines and 200 names.

`wb-hostd` refuses any other manifest, and logs the line and the rule.
Examples are a manifest with a `tap`, a `cask`, options or `system`, a
name with a `/`, or a name starting with `-`. A name with a `/` is a
tap's formula, so only `homebrew/core` formulae are allowed.

`wb-guestd` checks the names against the same rules before it runs
anything. It never runs `brew bundle`, and never hands Homebrew a file
a project wrote.

#### The install

1. `wb-guestd` resolves the whole runtime closure of the names over the
   formula data snapshot, as the toolchain user: `brew deps --formula
   -n --union -- <names>`.
2. It checks every name in the closure against the same rules.
3. It runs `brew install --formula --force-bottle -- <closure>`, by
   argument list, with no shell, in the toolchain user's home.

Homebrew applies `--force-bottle` and `HOMEBREW_NO_INSTALL_UPGRADE`
only to the formulae on the command line. So every keg is named there.

Any nonzero exit of a `brew` command refuses the session. No later
step relies on what a failed run left in the prefix.

#### Bottles only

Before the install, `wb-guestd` checks the formula data for a bottle
for the guest, for every formula in the closure. When one has none,
the session is refused with its name, before anything is installed.
Nothing is built from source.

When the formula data snapshot is missing or empty, `wb-guestd`
refuses the reconcile, because Homebrew would download new data
.

At the start deadline (S06-09-session-lifecycle), `wb-guestd` kills
the reconcile's process group.

#### The environment

`wb-guestd` builds the toolchain user's
environment from an allowlist:

- `HOME`, and the working directory in its home;
- umask 022;
- `PATH` with the prefix's `bin` and the system folders;
- `HOMEBREW_NO_AUTO_UPDATE`, `HOMEBREW_NO_INSTALL_UPGRADE`,
  `HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK`,
  `HOMEBREW_NO_INSTALL_CLEANUP`, `HOMEBREW_NO_ANALYTICS`,
  `HOMEBREW_NO_ENV_HINTS` and `HOMEBREW_FORBID_PACKAGES_FROM_PATHS=1`;
- `HOMEBREW_TEMP` and `HOMEBREW_CACHE` in its home;
- the trust variables of S09-08-guest-trust, with the same constant
  values project users get, never appended to an inherited value.

It never sets `HOMEBREW_DEVELOPER`.

#### A reconcile only adds

Homebrew upgrades an outdated dependency of a
formula it installs, whatever `HOMEBREW_NO_INSTALL_UPGRADE` says,
unless that dependency is named on the command line. "The install"
makes sure it is.

The formula data keeps dependencies, such as `openssl@3`, from moving
under a running session:

- The image build writes a snapshot of Homebrew's formula API into the
  toolchain user's cache.
- `HOMEBREW_NO_AUTO_UPDATE` keeps that snapshot.

Then nothing installed is outdated, and a reconcile only adds kegs.

#### The session's `PATH`

Which version a name like `python3` runs in
the prefix's shared `bin` depends on every project's formulae.

The session's `PATH` has `opt/<formula>/bin`, and Python's
`libexec/bin`, for each formula of the image's recipe (I150) and of the
project's manifest. It doesn't have the shared `bin`. A project can add
the shared `bin` back, so this is for reproducible sessions only.

`wb-guestd` sets `NPM_CONFIG_PREFIX` to a folder in the home, because
`npm -g` writes to the prefix by default.

#### Conflicts

Homebrew can refuse a formula because a conflicting one is installed,
such as `mysql` and `mariadb`. The reconcile then fails. `wb` refuses
the session, names both formulae, and points to `--isolated`
(NFR06-explained-refusals).

#### A new system disk

The prefix is on the system disk. So a new system disk loses what
project layers added, and the next session start installs it again.

**Status:** Draft
