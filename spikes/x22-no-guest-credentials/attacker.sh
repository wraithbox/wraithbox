#!/bin/sh
# Throwaway: what an attacker in the guest would try with a token of its
# own. The token here is fake, so upstream always refuses; what matters is
# whether the proxy forwards it (results/<mode>.jsonl, "creds").
# Exit status: 0 if the proxy forwarded no attacker credential upstream on
# a write; checked by summarize.py, not here.
T="${X22_FAKE:-wbattacker000000000000000000000000000000}"
c() { printf '%s: ' "$1"; shift; curl -s -o /dev/null -w '%{http_code}\n' "$@"; }
c "github API create repo (POST, token)" -X POST -H "Authorization: token $T" -d '{"name":"x22"}' https://api.github.com/user/repos
c "github API read own user (GET, token)" -H "Authorization: token $T" https://api.github.com/user
c "github API create gist (POST, bearer)" -X POST -H "Authorization: Bearer $T" -d '{"files":{"a":{"content":"x"}}}' https://api.github.com/gists
c "github graphql mutation (POST)" -X POST -H "Authorization: bearer $T" -d '{"query":"mutation{addStar(input:{starrableId:\"x\"}){clientMutationId}}"}' https://api.github.com/graphql
c "ghcr token with Basic creds (GET)" -u "attacker:$T" 'https://ghcr.io/token?service=ghcr.io&scope=repository:attacker/x:pull,push'
c "ghcr blob upload (POST, bearer)" -X POST -H "Authorization: Bearer $T" https://ghcr.io/v2/attacker/x/blobs/uploads/
c "npm publish (PUT, bearer)" -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{}' https://registry.npmjs.org/x22-spike-never
c "pypi upload (POST, basic)" -X POST -u "__token__:$T" https://upload.pypi.org/legacy/
c "anthropic api with own key (POST, x-api-key)" -X POST -H "x-api-key: $T" -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' -d '{}' https://api.anthropic.com/v1/messages
c "cookie session (GET, cookie)" -H "Cookie: user_session=$T" https://github.com/settings/profile
# An anonymous token fetched through the proxy (recorded by the issued rule),
# then used for a write.
curl -s -o /dev/null https://ghcr.io/v2/homebrew/core/jq/tags/list
A=$(curl -s 'https://ghcr.io/token?service=ghcr.io&scope=repository:homebrew/core/jq:pull' | sed -E 's/.*"token":"([^"]+)".*/\1/')
c "ghcr blob upload with an issued anonymous token (POST)" -X POST -H "Authorization: Bearer $A" https://ghcr.io/v2/homebrew/core/jq/blobs/uploads/
c "issued anonymous token on another host (GET)" -H "Authorization: Bearer $A" https://api.github.com/user
