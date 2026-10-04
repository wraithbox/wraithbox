#!/bin/sh
# Throwaway: a registry client that follows the token flow of the
# distribution spec, as docker, oras and crane do (none is installed here).
# GET -> 401 with a realm -> GET the realm anonymously -> GET with the token.
set -e
R=https://ghcr.io/v2/homebrew/core/jq/manifests/1.8.2
ACCEPT='Accept: application/vnd.oci.image.index.v1+json'
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$ACCEPT" "$R")
echo "first GET: $code"
if [ "$code" = 200 ]; then exit 0; fi
wa=$(curl -s -o /dev/null -D - -H "$ACCEPT" "$R" | tr -d '\r' | grep -i '^www-authenticate:')
realm=$(echo "$wa" | sed -E 's/.*realm="([^"]+)".*/\1/')
service=$(echo "$wa" | sed -E 's/.*service="([^"]+)".*/\1/')
scope=$(echo "$wa" | sed -E 's/.*scope="([^"]+)".*/\1/')
tok=$(curl -s "$realm?service=$service&scope=$scope" | sed -E 's/.*"token":"([^"]+)".*/\1/')
echo "token: ${#tok} chars"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$ACCEPT" -H "Authorization: Bearer $tok" "$R")
echo "GET with token: $code"
[ "$code" = 200 ]
