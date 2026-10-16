# S17-05 - Upgrades and Migrations

**Purpose:** How an upgrade replaces the bundle, and how `wb-hostd` moves
stored data to a new version.

## Design

### Upgrades

An upgrade replaces the bundle at its path. `app:install`, and later
the Homebrew cask:

1. copy the new bundle next to the old one;
2. rename the old one aside;
3. rename the new one in;
4. delete the old one.

They never copy over the files of an installed bundle. So launchd or
`wb-launcher` never starts a program from a bundle that is half old
and half new. Running processes keep their open files.

Then S04-07-supervision, "Upgrades", applies. `wb-hostd` sees a new
`WBBuildVersion` and exits, and launchd starts the new one. The guest
side updates from the guest tools disk at each VM's next cold start
(S06-12-guest-agent, "Update at start").

### Another signing team

A build signed by another team has another bundle identifier and
other access groups (S15-least-privilege). So it can't read the old
team's store items.

- The user sets the bindings again with `wb cred set`.
- Each VM start is refused with `ca-key-restore` until `wb vm
  reset-ca`.

### Migrations

Only `wb-hostd` migrates. It does so at start, after it confines
itself and before it serves a request. `wb` and the other processes
never migrate.

#### `state.db`

The schema version of `state.db` is `PRAGMA user_version`. Numbered,
forward-only migrations are compiled into `wb-hostd`. Each runs in a
transaction that also sets the new version.

#### The backup

Before the first pending migration, `wb-hostd` backs up `state.db`:

1. It writes a copy with `VACUUM INTO` to a new temporary name in
   `<data>`, mode 600.
2. It links the copy to the name `state.db.v<N>.bak`, where `<N>` is
   the version the copy holds, read from the copy, and removes the
   temporary name. A link fails on an existing name, where a rename
   would replace the file.

It never writes over an existing `.bak` file. When that name exists,
the copy from an earlier attempt stays. It keeps the two newest
copies. A database that this start created has nothing to back up, and
`wb-hostd` doesn't copy it.

#### Configuration

No process rewrites a file in `<config>`. A missing `config.toml` means
every default. `wb-hostd` refuses:

- a file with a key that a release renamed or removed, with
  `config-key-renamed`, naming the file, the line and the new key;
- a file with any other unknown key, at any level, with
  `config-key-unknown`, naming the file, the line and the key. A key
  matches only when spelled exactly, case included;
- a known key with a value of the wrong type or out of range, with
  `config-value-invalid`;
- a file that isn't valid TOML, or that defines a key or table twice,
  with `config-syntax-invalid`;
- a file it can't read, that isn't a regular file, or that is too
  large, with `config-file-unreadable`.

#### Refusing mode

These put `wb-hostd` in refusing mode:

- a failed backup (`state-db-backup-failed`);
- a failed migration (`state-db-migration-failed`);
- a database newer than `wb-hostd` knows (`state-db-newer`);
- a database it can't open or read the version of
  (`state-db-open-failed`);
- a refused configuration file.

In refusing mode, `wb-hostd` keeps running, so launchd doesn't restart
it in a loop. It doesn't start a daemon or a VM. It answers `wb
status`. It refuses every other request with the rule. The refusal
names the versions, the file and line, or the newest copy.

#### Not migrated

- Secret store item names are fixed (S15-least-privilege).
- Images aren't migrated.
- A saved state of another build version is deleted
  (S06-12-guest-agent, "The host checks, not the guest").

**Status:** Draft
