# X19-terminal-filter spike (throwaway)

Throwaway code for X19-terminal-filter (#30). Not held to the project
gates, never merged. The result is in `docs/spikes/X19-terminal-filter.md`
on `main`.

All commands run from this directory with `GOWORK=off` (the module is not
in the repository's `go.work`).

## Relay (for the check by eye, #62)

```sh
GOWORK=off go run ./cmd/relay -log drops.txt -- claude        # filtered
GOWORK=off go run ./cmd/relay -raw -- claude                  # unfiltered baseline
GOWORK=off go run ./cmd/relay -- ./scripts/hostile.sh         # sequences that act on the host
```

The relay runs the command in a PTY, puts this terminal in raw mode, and
writes the command's output through `filter/`. On exit it prints the
number of sequences it dropped or rewrote, by rule. `-log` writes each
one.

## Pieces

- `filter/`: tokenizer and allowlist (`token.go`, `filter.go`), table
  tests with violations, a fuzz target, benchmarks.
- `cmd/record`: runs a program in a PTY behind a headless terminal
  emulator (`charmbracelet/x/vt`), drives it from a script in
  `scripts/`, answers its terminal queries as a chosen terminal would
  (`-profile ghostty|apple|iterm|vscode`), and writes an asciicast v2
  file to `recordings/`.
- `cmd/classify`: inventory of every sequence in recordings, with the
  filter's decision.
- `cmd/compare`: replays each recording into two emulators, raw and
  filtered, and compares the screens after every chunk.

```sh
GOWORK=off go build -o bin/ ./cmd/...
./scripts/run-all.sh "$PWD/../../.scratch"      # records the session script per profile
./bin/classify recordings/*.cast
./bin/compare recordings/*.cast
GOWORK=off go test ./filter/ -fuzz FuzzFilter -fuzztime 120s
GOWORK=off go test ./filter/ -run XXX -bench .
```
