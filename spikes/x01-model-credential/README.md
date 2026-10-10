# X01-model-credential spike: API key mode via the proxy

Throwaway code for issue #60. Not held to the project gates, never merged.

- `proxy/`: `x01-proxy`, a TLS-inspecting HTTP CONNECT proxy. It makes a
  CA at start, writes it to `-ca-out`, logs every host and request
  (method, host, path, header names), and on the `-inject` hosts
  replaces the placeholder in `x-api-key` or `Authorization` with the
  real key. It reads the real key with
  `security find-generic-password -s wraithbox-secret -w` from the
  Keychain of the user it runs as. It never logs the key.
  `GOWORK=off go test ./...` checks the injection against a fake API.
- `run.sh`: the test, run as `wbtest`. `-p`, `-p --resume`, an
  interactive session and an interactive `--resume`, all with a
  placeholder `ANTHROPIC_API_KEY`, then the host list and a search of
  `wbtest`'s home for the real key.

## Status

Not run. Running anything as `wbtest` from the agent's session needs
`sudo` with a password, so the run waits on the maintainer (issue #60).

## Run it

As the maintainer, in a terminal logged in as `wbtest` (Fast User
Switching, or `su - wbtest` from a terminal followed by
`security unlock-keychain`, which asks for `wbtest`'s password):

```sh
# as lsimons, from the spike branch checkout
cd spikes/x01-model-credential/proxy && GOWORK=off go build -o x01-proxy .
# as wbtest
mkdir -p ~/x01
cp <checkout>/spikes/x01-model-credential/proxy/x01-proxy ~/x01/
cp <checkout>/spikes/x01-model-credential/run.sh ~/x01/
cp "$(readlink ~lsimons/.local/bin/claude)" ~/x01/claude   # or install Claude Code as wbtest
~/x01/run.sh
```

The logs land in `~wbtest/x01/out/`. They hold no key: the proxy logs
header names and `key=injected`, and the disk search prints file names.
