## Client runs (exit status 0 = pass)

| Operation | observe | strip | read-only | issued | inject | inject-flow |
|---|---|---|---|---|---|---|
| brew-fetch | pass | FAIL (1) | pass | FAIL (1) | pass | pass |
| brew-fetch+ph | pass | FAIL (1) | pass | FAIL (1) | pass | pass |
| git-clone | pass | pass | pass | pass | pass | pass |
| git-clone+ph | pass | pass | pass | pass | pass | pass |
| git-push-dry | FAIL (128) | FAIL (128) | FAIL (128) | FAIL (128) | FAIL (128) | FAIL (128) |
| git-lfs | pass | pass | pass | pass | pass | pass |
| gh | FAIL (4) | FAIL (4) | FAIL (4) | FAIL (4) | FAIL (4) | FAIL (4) |
| gh+ph | FAIL (1) | pass | FAIL (1) | pass | pass | pass |
| npm | pass | pass | pass | pass | pass | pass |
| npm+ph | pass | pass | pass | pass | pass | pass |
| pip | pass | pass | pass | pass | pass | pass |
| pip+ph | pass | pass | pass | pass | pass | pass |
| uv | pass | pass | pass | pass | pass | pass |
| uv+ph | pass | pass | pass | pass | pass | pass |
| go | pass | pass | pass | pass | pass | pass |
| go+ph | pass | pass | pass | pass | pass | pass |
| go-direct | pass | pass | pass | pass | pass | pass |
| cargo | pass | pass | pass | pass | pass | pass |
| cargo+ph | pass | pass | pass | pass | pass | pass |
| swiftpm | pass | pass | pass | pass | pass | pass |
| claude | FAIL (1) | FAIL (1) | - | - | - | - |
| claude+ph | FAIL (1) | FAIL (1) | - | - | - | - |
| oci-flow | pass | FAIL (1) | pass | pass | pass | pass |
| attacker | pass | pass | pass | pass | pass | pass |

## Attacker credentials: forwarded upstream?

| Request | observe | strip | read-only | issued | inject | inject-flow |
|---|---|---|---|---|---|---|
| `POST api.github.com/user/repos` | forward, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 |
| `GET api.github.com/user` | forward, 401 | strip, 401 | forward, 401 | strip, 401 | strip, 401 | strip, 401 |
| `POST api.github.com/gists` | forward, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 |
| `POST api.github.com/graphql` | forward, 401 | strip, 403 | strip, 403 | strip, 403 | strip, 403 | strip, 403 |
| `GET ghcr.io/token` | forward, 403 | strip, 403 | forward, 403 | strip, 403 | strip, 403 | strip, 403 |
| `POST ghcr.io/v2/attacker/x/blobs/uploads/` | forward, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 |
| `PUT registry.npmjs.org/x22-spike-never` | forward, 404 | strip, 404 | strip, 404 | strip, 404 | strip, 404 | strip, 404 |
| `POST upload.pypi.org/legacy/` | forward, 405 | strip, 405 | strip, 405 | strip, 405 | strip, 405 | strip, 405 |
| `POST api.anthropic.com/v1/messages` | forward, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 | strip, 401 |
| `GET github.com/settings/profile` | forward, 302 | strip, 302 | forward, 302 | strip, 302 | strip, 302 | strip, 302 |
| `GET ghcr.io/v2/homebrew/core/jq/tags/list` | none sent, 401 | none sent, 401 | none sent, 401 | none sent, 401 | none sent, 200 | none sent, 200 |
| `GET ghcr.io/token #2` | none sent, 200 | none sent, 200 | none sent, 200 | none sent, 200 | none sent, 200 | none sent, 200 |
| `POST ghcr.io/v2/homebrew/core/jq/blobs/uploads/` | forward, 401 | strip, 401 | strip, 401 | strip, 401 | refused (403) | refused (403) |
| `GET api.github.com/user #2` | forward, 401 | strip, 401 | forward, 401 | strip, 401 | strip, 401 | strip, 401 |

## Hosts, credentials and cookies seen with the rule off (observe)

| Operation | Host | Requests | Credential headers sent | Other key-like headers | Set-Cookie | 401 challenge |
|---|---|---|---|---|---|---|
| brew-fetch | formulae.brew.sh | 1 | - | - | - | - |
| brew-fetch | ghcr.io | 4 | Authorization Bearer | - | - | - |
| brew-fetch | pkg-containers.githubusercontent.com | 2 | - | - | - | - |
| brew-fetch (placeholder) | formulae.brew.sh | 1 | - | - | - | - |
| brew-fetch (placeholder) | ghcr.io | 4 | Authorization Bearer | - | - | - |
| brew-fetch (placeholder) | pkg-containers.githubusercontent.com | 2 | - | - | - | - |
| git-clone | github.com | 3 | - | - | - | - |
| git-clone (placeholder) | github.com | 3 | - | - | - | - |
| git-push-dry | github.com | 5 | Authorization Basic | - | - | Basic |
| git-lfs | github.com | 4 | - | - | - | - |
| git-lfs | github-cloud.githubusercontent.com | 8 | - | - | - | - |
| gh (placeholder) | api.github.com | 1 | Authorization token | - | - | - |
| npm | registry.npmjs.org | 4 | - | Npm-Auth-Type | __cf_bm, _cfuvid | - |
| npm (placeholder) | registry.npmjs.org | 4 | Authorization Bearer | Npm-Auth-Type | __cf_bm, _cfuvid | - |
| pip | pypi.org | 2 | - | - | - | - |
| pip | files.pythonhosted.org | 4 | - | - | - | - |
| pip (placeholder) | pypi.org | 2 | Authorization Basic | - | - | - |
| pip (placeholder) | files.pythonhosted.org | 4 | - | - | - | - |
| uv | pypi.org | 2 | - | - | - | - |
| uv | files.pythonhosted.org | 4 | - | - | - | - |
| uv (placeholder) | pypi.org | 2 | Authorization Basic | - | - | - |
| uv (placeholder) | files.pythonhosted.org | 4 | - | - | - | - |
| go | proxy.golang.org | 4 | - | - | - | - |
| go | sum.golang.org | 8 | - | - | - | - |
| go (placeholder) | proxy.golang.org | 4 | - | - | - | - |
| go (placeholder) | sum.golang.org | 8 | - | - | - | - |
| go-direct | proxy.golang.org | 5 | - | - | - | - |
| go-direct | sum.golang.org | 8 | - | - | - | - |
| cargo | index.crates.io | 3 | - | - | - | - |
| cargo | static.crates.io | 2 | - | - | - | - |
| cargo (placeholder) | index.crates.io | 3 | - | - | - | - |
| cargo (placeholder) | static.crates.io | 2 | - | - | - | - |
| swiftpm | github.com | 3 | - | - | - | - |
| claude | api.anthropic.com | 29 | X-Api-Key | X-Claude-Code-Session-Id | - | - |
| claude | http-intake.logs.us5.datadoghq.com | 2 | - | Dd-Api-Key | - | - |
| claude (placeholder) | api.anthropic.com | 29 | X-Api-Key | X-Claude-Code-Session-Id | - | - |
| claude (placeholder) | http-intake.logs.us5.datadoghq.com | 2 | - | Dd-Api-Key | - | - |
| oci-flow | ghcr.io | 4 | Authorization Bearer | - | - | Bearer |

Cookie headers sent by any client in any mode (attacker script excluded): 0
