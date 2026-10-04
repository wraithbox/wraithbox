# X07-git-round-trip spike (throwaway)

Throwaway code for spike X07-git-round-trip (#21). Not held to the
project gates, never merged. The written result is
`docs/spikes/X07-git-round-trip.md` on `main`.

- `cmd/git-remote-wb`: guest-side remote helper with the `connect`
  capability. It reaches the host through an inherited socketpair end
  (`WB_FD`) instead of vsock.
- `host`: the host side. Reads a one-line header, runs `git upload-pack`
  (read-only, hidden refs, scrubbed environment) or `git receive-pack`
  (hooks off, strict fsck, size limit, hidden refs) behind `pktfilter`.
- `pktfilter`: the ref restriction. Reads the receive-pack command list
  and refuses the whole push unless every ref is under
  `refs/heads/wb/<session-id>/`. Fuzz target `FuzzReadRequest`.
- `flagger`: risky-path flagger over `git diff-tree -r -z --raw`. Fuzz
  target `FuzzParseRaw`.
- `cmd/x07`: the harness. Clones the Go repository once into
  `$TMPDIR/x07-cache`, runs everything else in a fresh `$TMPDIR/x07-run-*`
  and deletes it at exit.
- `results/`: the output of the runs quoted in the result. `run1.txt`
  is partial (a harness bug hung the raw phase) and predates the
  helper asking for protocol v2, so every fetch in it is v0, whatever
  its label says. `run2.txt` ran under heavy load from other agents'
  VMs. `run3-apple.txt` is Apple git 2.54.0 on both sides.

```sh
cd spikes/x07-git-round-trip
GOWORK=off go test ./...
GOWORK=off go run ./cmd/x07                       # Homebrew git both sides
GOWORK=off go run ./cmd/x07 -bothgit /usr/bin/git # Apple git both sides
```
