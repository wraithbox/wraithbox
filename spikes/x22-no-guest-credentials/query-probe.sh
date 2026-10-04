#!/bin/sh
# Throwaway: do git hosts read a credential from somewhere other than the
# Authorization header? A bogus value that the host reads gives 401; one it
# ignores gives the anonymous answer (200). No account is used.
B=wbattacker000000000000000000000000000000
c() { printf '%s: ' "$1"; shift; curl -s -o /dev/null -w '%{http_code}\n' "$@"; }
c "github api, no credential" https://api.github.com/repos/cli/cli
c "github api, ?access_token=" "https://api.github.com/repos/cli/cli?access_token=$B"
c "github api, Authorization: token" -H "Authorization: token $B" https://api.github.com/repos/cli/cli
c "gitlab api, no credential" https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner
c "gitlab api, ?private_token=" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner?private_token=$B"
c "gitlab api, ?access_token=" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner?access_token=$B"
c "gitlab api, PRIVATE-TOKEN header" -H "PRIVATE-TOKEN: $B" https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner
c "gitlab api, JOB-TOKEN header" -H "JOB-TOKEN: $B" https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner
c "gitlab api, Authorization: Bearer" -H "Authorization: Bearer $B" https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner
c "bitbucket api, no credential" https://api.bitbucket.org/2.0/repositories/atlassian/python-bitbucket
c "bitbucket api, ?access_token=" "https://api.bitbucket.org/2.0/repositories/atlassian/python-bitbucket?access_token=$B"
c "npm registry, ?token= ignored?" "https://registry.npmjs.org/is-number?token=$B"
c "huggingface api, no credential" https://huggingface.co/api/models/gpt2
c "huggingface api, Authorization: Bearer" -H "Authorization: Bearer hf_$B" https://huggingface.co/api/models/gpt2
