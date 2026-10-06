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
