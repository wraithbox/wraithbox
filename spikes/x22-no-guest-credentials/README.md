# X22-no-guest-credentials spike (throwaway)

Throwaway code for issue #33. Not held to the project gates, never merged.
The written result is `docs/spikes/X22-no-guest-credentials.md` on `main`.

Run on 2026-10-05, macOS 27.0.1 on Apple Silicon, against the public
services, with no account: every credential in these runs is fake or
anonymous. Claude Code's own login is X01-model-credential's and was not
tested.

Client versions: Homebrew 7.0.7, git 2.56.0, git-lfs 3.8.0, gh 2.102.0,
npm 12.2.0, pip 26.2.1, uv 0.12.23, go 1.27.1, cargo 1.99.0,
Swift 6.4 (SwiftPM), Claude Code 2.1.289.

## What is here

- `proxy/main.go`: an inspecting HTTPS proxy on 127.0.0.1 with a throwaway
  CA (generated per run, trusted only per process through `SSL_CERT_FILE`,
  `NODE_EXTRA_CA_CERTS`, `GIT_SSL_CAINFO`, `PIP_CERT`, `CARGO_HTTP_CAINFO`
  and a curlrc for Homebrew, deleted with the temp dir). It applies one
  rule to the credential headers (`Authorization`, `Proxy-Authorization`,
  `Cookie` and common API-key headers) of every request:
  - `observe`: forward unchanged (baseline).
  - `strip`: remove all of them (S07-egress-gateway before this spike).
  - `read-only`: remove them, except on GET and HEAD.
  - `issued`: remove them, except a bearer token that the proxy saw a
    registry's token endpoint issue in this run, to a request the proxy
    had stripped, on GET/HEAD to the host that named that endpoint.
  - `inject`: remove them, then inject a host-side anonymous credential on
    `ghcr.io` `/v2/homebrew/core/` reads: `Bearer QQ==` (`-inject-kind
    static`, what Homebrew sends) or a token the proxy fetches itself from
    `ghcr.io/token` with no credentials (`-inject-kind flow`).
  Header values are never logged, only the scheme and a hash prefix.
- `harness.py`: each client's read operations, once per rule, each in a
  fresh temp home and cache. The `+ph` variant configures a fake token the
  way a guest holds a placeholder (npm `_authToken`, pip and uv index URL
  credentials, `GH_TOKEN`, `.netrc` for Go, `CARGO_REGISTRY_TOKEN`,
  `HOMEBREW_GITHUB_API_TOKEN`, git URL credentials).
- `oci-flow.sh`: a registry client that follows the distribution spec's
  token flow (as docker, oras and crane do; none is installed here).
- `attacker.sh`: writes with a token of the attacker's own, and an
  anonymous token fetched through the proxy then used for a write.
- `ghcr-probe.sh`, `query-probe.sh`: how `ghcr.io` treats anonymous, bogus
  and issued tokens, and which git hosts read a token from the query
  string.
- `summarize.py`, `analyze.py`: the tables in `results-tables.md`.

## Results

- `results-matrix.txt`, `results-extra.txt`: one line per run.
- `results-tables.md`: pass/fail per client and rule; whether an attacker
  credential was forwarded; hosts, credential headers and cookies seen.
- `results-ghcr-probe.txt`, `results-query-probe.txt`: the probes.
- `results/*.jsonl`: the proxy's event logs, one file per rule.

## Notes

- Go 1.27.1 on this macOS honored `SSL_CERT_FILE` (`go-direct` and `gh`
  reached the inspecting proxy), unlike the X21-dep-gate-registries runs.
- Claude Code retries a 401 from the model API for about three minutes,
  so it ran only under `observe` and `strip`.
- The LFS repository is `github.com/niik/lfs-test` (four objects, 2 to
  26 MB).
- `review-probe.sh`, `results-review-probe.txt` (added in the review of
  PR #85): GitLab form and JSON body credentials, percent-encoded and
  upper-case parameter names, `job_token`, `Deploy-Token`, and Forgejo on
  `codeberg.org`.
