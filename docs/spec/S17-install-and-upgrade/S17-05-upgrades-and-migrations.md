# S17-05 - Upgrades and Migrations

**Purpose:** How an upgrade replaces the bundle, and how `wb-hostd` moves
stored data to a new version.

## Design

### Upgrades

An upgrade replaces the bundle at its path. `app:install`, and later the
Homebrew cask, copy the new bundle next to the old one, rename the old
one aside, rename the new one in, and delete the old one. They never
copy over the files of an installed bundle, so launchd or `wb-launcher`
never starts a program from a bundle that is half old and half new.
Running processes keep their open files. Then
S04-07-supervision, "Upgrades", applies: `wb-hostd` sees a new
`WBBuildVersion`, exits, and launchd starts the new one. The guest
side updates from the guest tools disk at each VM's next cold start
(S06-vm-lifecycle).

A build signed by another team has another bundle identifier and other
access groups (S15-least-privilege), so it can't read the old team's
store items. The user sets the bindings again with `wb cred set`, and
each VM start is refused with `ca-key-restore` until `wb vm reset-ca`.

### Migrations

Only `wb-hostd` migrates, at start, after it confines itself and before
it serves a request. `wb` and the other processes never do.

- **`state.db`.** Its schema version is `PRAGMA user_version`. Numbered,
  forward-only migrations are compiled into `wb-hostd`, each in a
  transaction that also sets the new version.
- **The backup.** Before the first pending migration, `wb-hostd` writes
  a copy with `VACUUM INTO` to a new temporary name in `<data>`, mode
  600, and renames it to `state.db.v<N>.bak`, `<N>` the version it had.
  It never writes over an existing `.bak` file: when that name exists,
  the copy from an earlier attempt stays. It keeps the two newest
  copies.
- **Configuration.** No process rewrites a file in `<config>`.
  `wb-hostd` refuses a file with a key that a release renamed or
  removed, with `config-key-renamed` and the file, the line and the new
  key, and a file with any other unknown key, with `config-key-unknown`.
- **Refusing mode.** A failed backup (`state-db-backup-failed`), a
  failed migration (`state-db-migration-failed`), a database newer than
  `wb-hostd` knows (`state-db-newer`), and a refused configuration file
  put `wb-hostd` in refusing mode. It keeps running, so launchd doesn't
  restart it in a loop. It doesn't start a daemon or a VM. It answers
  `wb status` and refuses every other request with the rule, naming
  the versions, the file and line, or the newest copy.
- Secret store item names are fixed (S15-least-privilege), and images
  aren't migrated. A saved state of another build version is deleted
  (S06-vm-lifecycle).

**Status:** Draft
