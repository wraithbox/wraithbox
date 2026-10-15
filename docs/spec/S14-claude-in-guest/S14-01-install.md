# S14-01 - Install

**Purpose:** How the image build downloads Claude Code on the host,
checks it, and installs it in the guest image, and which version a
guest runs.

## Design

Only the host downloads Claude Code. The guest never fetches it.
`downloads.claude.ai` is outside every profile the guest gets
(S07-07-http-policy, "model API", "Outside the profile").

### Download

During `wb image build`, `wb-download` (S17-install-and-upgrade,
"Downloads") fetches three files from
`https://downloads.claude.ai/claude-code-releases/<version>/`:

- the release's `manifest.json`;
- its detached signature, `manifest.json.sig`;
- the guest's binary.

For a macOS guest, the binary is at `<version>/darwin-arm64/claude`.
That path is compiled into Wraith Box for each guest platform, so
`wb-download` doesn't read the manifest.

The binary is one uncompressed Mach-O executable. Anthropic PBC signs
it, and Apple notarizes it.

`wb-hostd` itself doesn't open an outbound connection.

### Check

`wb-release-check` (S04-architecture) checks the files. `wb-hostd`
starts this per-run program through `wb-launcher`.

- It reads the files on inherited descriptors.
- It writes a fixed-format record to a pipe. The record is `pass` with
  the manifest's signed SHA-256 checksum for the guest's platform, or
  the rule of the failed check.

`wb-hostd` doesn't parse the signature or the manifest. They are bytes
from the network, and the process that faces the guest doesn't run a
third-party parser over them (SEC12-least-privilege).

The check follows Anthropic's "Binary integrity and code signing"
(https://code.claude.com/docs/en/setup). It checks that:

1. The detached signature verifies over `manifest.json` with
   Anthropic's Claude Code release key.
   - The full public key is compiled into Wraith Box. Wraith Box never
     downloads the key.
   - Its fingerprint is
     `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`.
2. The manifest's `version` equals the version the build asked for. An
   older signed release served under a newer path then fails.
3. The version is at least the minimum compiled into Wraith Box,
   2.1.289.
   - That is the Claude Code version that X01-model-credential measured
     the model API profile with.
   - Releases before 2.1.89 have no signature, and this minimum refuses
     them too.
4. The manifest has an entry for the guest's platform, `darwin-arm64`
   for a macOS guest.
5. That entry's `binary` field is the file name the download used,
   `claude`.
6. The binary's size and SHA-256 equal that entry's `size` and
   `checksum`.

Any failed check fails the build. The build logs the check that
failed. The manifest parser has a fuzz target.

### The checked bytes

`wb-hostd` opens the binary once and keeps that descriptor. Then:

1. It `fstat`s the descriptor. It refuses a file that isn't a regular
   file, with the rule `claude-release-bytes-changed`.
2. It passes a duplicate of the descriptor to `wb-release-check`.
3. After a `pass`, it reads the file with `pread` from offset 0. It
   uses `pread` because the duplicate shares the child's file offset.
4. It streams the file to `wb-guestd`, and hashes it as it sends.
5. Only after the last byte, it sends the SHA-256 from the record.

`wb-guestd` writes the stream to a temporary file that only root can
write. It installs the file only when both hold:

- the SHA-256 of what it received equals the one `wb-hostd` sent;
- `wb-hostd` reports that its own hash of the sent bytes matched too.

A file changed between the check and the send fails the build, with
the rule `claude-release-bytes-changed`.

### Into the image

`wb-guestd` installs the binary through a named operation of the base
layer, before sealing (S06-02-image-build, "Built by Wraith Box").

- It installs it as `/usr/local/bin/claude`, owned by root, with mode
  755.
- The directory is one that project users can't write to.
- The toolchain user can't write it either, so a project's formulae
  can't replace it (S06-01-image-layers, "Toolchain prefix").

`wb-guestd` starts Claude Code by that absolute path, never through
`PATH`.

### Version

The image sets the Claude Code version. The installed binary can't
change:

- project users can't write it;
- `DISABLE_UPDATES=1` blocks every update path
  (S06-08-projects-and-sessions, "Claude Code in the guest").

The profile is the control. An agent can still install another Claude
Code in its own home, for example with `npm`. `wb-guestd` doesn't
start that one. An updater would fetch and run a binary past
SEC07-dep-gate.

A new version arrives with a new image. `wb image list` shows each
image's Claude Code version.

I220 decides which release a build picks. It also decides how a build
for a new Claude Code version costs less than a full build.

### Version skew

The host's Claude Code and the guest's can differ, and nothing
compares them. Only configuration files cross
(S14-02-user-configuration). How the guest's build treats a key it
doesn't know is up to that build.

## Decisions

### Only the host downloads Claude Code

- **Decided:** I45, row 1.

**Status:** Draft
