# X28-git-alloc-limit spike (throwaway)

Throwaway code for spike X28-git-alloc-limit (#102). Not held to the
project gates, never merged. The written result is
`docs/spikes/X28-git-alloc-limit.md` on `main`.

The code is the X26-pre-receive-check harness, copied unchanged from
`spike/x26-pre-receive-check` at `077a8da` (the Go module is still
called `x26`), plus:

- `cmd/x26/alloc.go`: two phases.
  - `alloc` pushes the X26 bomb packs, and a few more (delta data
    length, a REF_DELTA against a large base in the borrowed repository,
    object counts in the pack header, a million tiny objects, 640 deltas
    under the cap), through `receive-pack` in five modes: the S08
    settings with and without `GIT_ALLOC_LIMIT=100m`, without the hook,
    with git's default `unpack-objects` path, and the S08 settings with
    the pack's 20-byte checksum left off. It records the result, time,
    CPU time, peak memory, and what the push left in `landing.git`.
  - `alloccost` pushes the Go repository's whole history, and one new
    commit into a landing repository that borrows from it, with and
    without the limit, to show that the limit refuses no real push.
- `cmd/x26/main.go`: `opts.allocLimit` and `opts.noTrailer`, and CPU
  time from `wait4`.
- `run-x28.sh`: both phases with Homebrew git and Apple git.
- `results/x28-run1-homebrew.txt` (git 2.56.0) and
  `results/x28-run2-apple.txt` (Apple git 2.54.0). The `run1` to `run4`
  files are X26's, unchanged.

```sh
cd spikes/x28-git-alloc-limit
sh run-x28.sh /path/to/bare/clone/of/golang/go
```

The X26 README follows, unchanged.

---

# X26-pre-receive-check spike (throwaway)

Throwaway code for spike X26-pre-receive-check (#74). Not held to the
project gates, never merged. The written result is
`docs/spikes/X26-pre-receive-check.md` on `main`. The branch starts from
`spike/x07-git-round-trip`, whose code is kept unchanged next to it.

- `cmd/wb-pre-receive` and `prereceive`: the pre-receive check. git runs
  it from a hooks directory the harness owns (`core.hooksPath`), with
  its limits in `WB_*` environment variables. It repeats the ref checks
  git makes only in `update()` (path and component length, old object
  id, directory/file and case conflicts), sums the inflated size of
  every object in the quarantine with `git cat-file --batch-check`, and
  can refuse objects no pushed ref reaches (`WB_CHECK_STRAY=1`). Its
  decision log goes to `WB_HOOK_LOG`, never to the guest.
- `packscan`: a Go scanner that reads the pack on its way to
  `receive-pack` and refuses it when an object's header size, or a
  delta's result size, is over the cap, before git allocates anything.
  Fuzz target `FuzzScan`.
- `packgen`: builds packs byte by byte, including delta and zlib bombs.
- `pktfilter`: copied unchanged from X07, the ref filter in front.
- `cmd/x26`: the harness. Phases `cases` (every refusal case, with and
  without the hook, files and reftable backends), `memory` (peak memory
  of `receive-pack` on bombs) and `cost` (the Go repository's whole
  history, and a one-commit push into a landing repository that
  borrows from it).
- `results/`: `run1-homebrew.txt` (git 2.56.0) and `run2-apple.txt`
  (Apple git 2.54.0).

```sh
cd spikes/x26-pre-receive-check
GOWORK=off go test ./...
sh run.sh /path/to/bare/clone/of/golang/go
```
