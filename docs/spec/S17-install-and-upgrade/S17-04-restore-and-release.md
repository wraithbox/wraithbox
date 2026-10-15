# S17-04 - Restore Image and Release

**Purpose:** The steps by which `wb-download` and `wb-hostd` fetch and check
a macOS restore image and a Claude Code release.

## Design

Each step runs under the rules of S17-03-downloads.

### The restore image

#### The entry

1. `wb-hostd` sends the host's macOS version.
2. `wb-download` fetches the catalog from
   `https://mesu.apple.com/assets/macos/com_apple_macOSIPSW/com_apple_macOSIPSW.xml`,
   at most 4 MiB, and parses it. The catalog parser has a fuzz target.
3. It picks the newest restore image not newer than the host's macOS.
4. It sends a `HEAD` for the image's size.
5. It reports the version, build, URL, SHA-1 and size.

#### The floor

`wb-hostd` refuses an entry older than the minimum compiled into
Wraith Box, with `restore-image-below-minimum`. The minimum is 27.0.1,
which X17-image-build built.

`state.db` records the macOS version of the last image a build sealed.
`wb-hostd` refuses an entry older than that version with
`restore-image-rollback`, unless the user passed `wb image build
--allow-older-macos`.

#### Resume or start over

`wb-hostd` keeps the entry of the partial file
`<data>/images/macos-restore.ipsw.partial` in `state.db`.

- When the new entry is the same, it answers with the partial file's
  size as the offset.
- Otherwise it truncates the file and answers 0.

It opens the file with `O_NOFOLLOW`, and creates it mode 600. It
requires a regular file from `fstat`. Then it passes the descriptor.

#### The fetch

- At offset 0, `wb-download` sends a plain `GET`. It requires a `200`
  with a `Content-Length` equal to the size.
- At a larger offset, it sends `Range: bytes=<offset>-`. It requires a
  `206` with exactly `Content-Range: bytes <offset>-<size − 1>/<size>`.

Anything else fails with `download-range-refused`. The
`Content-Range` parser has a fuzz target.

`wb-download` writes from the offset, and stops reading at the size. A
shorter body fails with `download-short`.

#### The check

After `wb-launcher` reports the exit, `wb-hostd`:

1. checks the size with `fstat` on its own descriptor;
2. hashes the file with SHA-1 from that descriptor;
3. renames it to `macos-restore.ipsw`.

A mismatch deletes the file, and fails the build with
`restore-image-hash-mismatch`.

Before each start of `wb-vmd-install`, `wb-hostd` checks the SHA-1
again, unless it did in the same build. A build that seals its image
deletes the restore image.

#### The trust base

The SHA-1 comes from a catalog fetched over TLS, checked against the
system trust store. It binds the file to that entry.

The catalog isn't signed, so a compromised `wb-download` can report
any entry. Against that, the design relies on Virtualization's
installer refusing a restore image that Apple didn't sign. That
property is unmeasured (I262).

### A Claude Code release

#### The version

The version is a build input that `wb-hostd` holds, from the user's
`wb image build`. It is never a value from a guest or a repository.
I220 decides which release a build picks.

`wb-hostd` and `wb-download` both refuse a version outside
`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}$`.

#### The files

At the build's start, `wb-hostd` deletes what an earlier build left in
`<data>/downloads/`, without following symbolic links.

It creates `manifest.json`, `manifest.json.sig` and `claude` there,
with `O_CREAT|O_EXCL|O_NOFOLLOW` and mode 600. It passes the three
descriptors.

#### The fetch

`wb-download` fetches:

| File | At most |
|---|---|
| `<version>/manifest.json` | 1 MiB |
| `<version>/manifest.json.sig` | 64 KiB |
| `<version>/darwin-arm64/claude` | 1 GiB |

`wb-download` parses none of the files. A larger file fails with
`download-too-large`. After the exit, `wb-hostd` checks each size with
`fstat`, and refuses the same way.

#### Exit before check

`wb-hostd` gives the files to `wb-release-check` only after
`wb-launcher` reports that `wb-download` exited. So nothing writes
them after the check. The build deletes them when it ends.

**Status:** Draft
