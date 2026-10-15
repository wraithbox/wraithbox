# S17-04 - Restore Image and Release

**Purpose:** The steps by which `wb-download` and `wb-hostd` fetch and check
a macOS restore image and a Claude Code release.

## Design

### The restore image

1. **The entry.** `wb-hostd` sends the host's macOS version.
   `wb-download` fetches the catalog from
   `https://mesu.apple.com/assets/macos/com_apple_macOSIPSW/com_apple_macOSIPSW.xml`,
   at most 4 MiB, and parses it. The catalog parser has a fuzz target.
   It picks the newest restore image not newer than the host's macOS,
   sends a `HEAD` for its size, and reports the version, build, URL,
   SHA-1 and size.
2. **The floor.** `wb-hostd` refuses an entry older than the minimum
   compiled into Wraith Box, 27.0.1, which X17-image-build built, with
   `restore-image-below-minimum`. It refuses an entry older than the
   macOS version of the last image a build sealed, which `state.db`
   records, with `restore-image-rollback`, unless the user passed
   `wb image build --allow-older-macos`.
3. **Resume or start over.** `wb-hostd` keeps the entry of the partial
   file `<data>/images/macos-restore.ipsw.partial` in `state.db`. When
   the new entry is the same, it answers with the partial file's size
   as the offset. Otherwise it truncates the file and answers 0. It
   opens the file with `O_NOFOLLOW`, creates it mode 600, requires a
   regular file from `fstat`, and passes the descriptor.
4. **The fetch.** At offset 0, `wb-download` sends a plain `GET` and
   requires a `200` with a `Content-Length` equal to the size. At a
   larger offset it sends `Range: bytes=<offset>-`, and requires a `206`
   with exactly `Content-Range: bytes <offset>-<size − 1>/<size>`.
   Anything else fails with `download-range-refused`. It writes from the
   offset, and stops reading at the size. A shorter body fails with
   `download-short`. The `Content-Range` parser has a fuzz target.
5. **The check.** After `wb-launcher` reports the exit, `wb-hostd`
   checks the size with `fstat` on its own descriptor, hashes the file
   with SHA-1 from that descriptor, and renames it to
   `macos-restore.ipsw`. A mismatch deletes it and fails the build with
   `restore-image-hash-mismatch`. Before each start of
   `wb-vmd-install`, `wb-hostd` checks the SHA-1 again unless it did in
   the same build. A build that seals its image deletes the restore
   image.
6. **The trust base.** The SHA-1 comes from a catalog fetched over TLS
   checked against the system trust store, and binds the file to that
   entry. The catalog isn't signed, so a compromised `wb-download` can
   report any entry. Against that, the design relies on Virtualization's
   installer refusing a restore image that Apple didn't sign. That
   property is unmeasured (I262).

### A Claude Code release

1. **The version** is a build input that `wb-hostd` holds, from the
   user's `wb image build` (I220 decides which release a build picks),
   never a value from a guest or a repository. `wb-hostd` and
   `wb-download` both refuse one outside
   `^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}$`.
2. **The files.** At the build's start `wb-hostd` deletes what an
   earlier build left in `<data>/downloads/`, without following
   symbolic links. It creates `manifest.json`, `manifest.json.sig` and
   `claude` there with `O_CREAT|O_EXCL|O_NOFOLLOW`, mode 600, and
   passes the three descriptors.
3. **The fetch.** `wb-download` fetches `<version>/manifest.json`, at
   most 1 MiB, `<version>/manifest.json.sig`, at most 64 KiB, and
   `<version>/darwin-arm64/claude`, at most 1 GiB. A larger file fails
   with `download-too-large`, and after the exit `wb-hostd` checks each
   size with `fstat` and refuses the same way. It parses none of them.
4. **Exit before check.** `wb-hostd` gives the files to
   `wb-release-check` only after `wb-launcher` reports that
   `wb-download` exited, so nothing writes them after the check. The
   build deletes them when it ends.

**Status:** Draft
