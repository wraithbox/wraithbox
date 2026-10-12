# X01-model-credential spike: API key mode via the proxy

Throwaway code for issue #60. Not held to the project gates, never merged.

- `proxy/`: `x01-proxy`, a TLS-inspecting HTTP CONNECT proxy. It makes a
  CA at start, writes it to `-ca-out`, logs every host and request
  (method, host, path, header names), and on the `-inject` hosts
  replaces the placeholder in `x-api-key` or `Authorization` with the
  real key. It reads the real key with
  `security find-generic-password -s wraithbox-secret -w` from the
  Keychain of the user it runs as. It never logs the key.
  It talks HTTP/2 upstream when the server offers it and rewrites each
  response to HTTP/1.1 for the client, chunked when the length is
  unknown, so SSE streams through.
  `GOWORK=off go test ./...` checks the injection against a fake API,
  over HTTP/1.1 and HTTP/2 upstreams, with a streaming case.
- `run.sh`: the test, run as `wbtest`. `-p`, `-p --resume`, an
  interactive session and an interactive `--resume`, all with a
  placeholder `ANTHROPIC_API_KEY`, then the host list and a search of
  `wbtest`'s home for the real key.

## Status

The maintainer runs it as `wbtest` (issue #214): the agent's session
can't switch to `wbtest` without a password. The first run injected
the key on every `api.anthropic.com` request, but Claude Code failed
with `Malformed_HTTP_Response`: the proxy wrote HTTP/2 status lines
(`HTTP/2.0 401`) to its HTTP/1.1 client. The second commit fixes that.
The rerun is pending.

The Keychain read needs the item created with `-T /usr/bin/security`.
With the default access list, `security find-generic-password -w`
exits 36 when no GUI can ask. A `sudo -u wbtest` session also needs
`security unlock-keychain` first.

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

When the API errors, `run.sh` can sit for minutes on one step with
no output: Claude Code retries before it gives up (the first run took
183 s to fail `-p`). Watch `out/proxy.log` to see it is still working.

The logs land in `~wbtest/x01/out/`. They hold no key: the proxy logs
header names and `key=injected`, and the disk search prints file names.
