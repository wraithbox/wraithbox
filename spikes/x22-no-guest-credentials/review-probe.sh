#!/bin/sh
# Throwaway, added in the review of PR #85: where else GitLab and Forgejo
# (codeberg.org) read a credential. A fake value the host reads gives 401,
# one it ignores gives the anonymous 200. Read-only, no account.
B=wbattacker000000000000000000000000000000
c(){ printf '%s: ' "$1"; shift; curl -s -o /dev/null -w '%{http_code}\n' "$@"; }
c "gitlab graphql form private_token" -X POST --data "private_token=$B&query={currentUser{id}}" https://gitlab.com/api/graphql
c "gitlab graphql json private_token" -X POST -H 'Content-Type: application/json' --data "{\"private_token\":\"$B\",\"query\":\"{currentUser{id}}\"}" https://gitlab.com/api/graphql
c "gitlab graphql json none" -X POST -H 'Content-Type: application/json' --data '{"query":"{currentUser{id}}"}' https://gitlab.com/api/graphql
c "gitlab ?private%5Ftoken" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner?private%5Ftoken=$B"
c "gitlab ?PRIVATE_TOKEN" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner?PRIVATE_TOKEN=$B"
c "gitlab ?job_token" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner?job_token=$B"
c "gitlab Deploy-Token" -H "Deploy-Token: $B" "https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab-runner"
c "codeberg none" https://codeberg.org/api/v1/repos/forgejo/forgejo
c "codeberg ?token" "https://codeberg.org/api/v1/repos/forgejo/forgejo?token=$B"
c "codeberg ?access_token" "https://codeberg.org/api/v1/repos/forgejo/forgejo?access_token=$B"
c "codeberg Authorization token" -H "Authorization: token $B" https://codeberg.org/api/v1/repos/forgejo/forgejo
