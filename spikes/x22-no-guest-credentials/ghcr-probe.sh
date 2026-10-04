#!/bin/sh
# Throwaway: how ghcr.io answers homebrew/core reads with no, anonymous,
# bogus and server-issued credentials, and whether its token endpoint
# takes credentials in a POST body. No account is used.
A=https://ghcr.io/v2/homebrew/core/wget/tags/list
M=https://ghcr.io/v2/homebrew/core/wget/manifests/latest
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
echo "tags/list, no Authorization: $(code "$A")"
curl -s -o /dev/null -D - "$A" | grep -i '^www-authenticate'
echo "tags/list, Bearer QQ==: $(code -H 'Authorization: Bearer QQ==' "$A")"
echo "tags/list, Bearer <random>: $(code -H 'Authorization: Bearer Zm9vYmFyYmF6' "$A")"
echo "manifest, Bearer QQ==: $(code -H 'Authorization: Bearer QQ==' -H 'Accept: application/vnd.oci.image.index.v1+json' "$M")"
echo "other public package (not homebrew), Bearer QQ==: $(code -H 'Authorization: Bearer QQ==' https://ghcr.io/v2/cli/cli/tags/list)"
T=$(curl -s 'https://ghcr.io/token?service=ghcr.io&scope=repository:homebrew/core/wget:pull' | sed -E 's/.*"token":"([^"]+)".*/\1/')
echo "anonymous token issued: ${#T} chars"
echo "tags/list, issued token: $(code -H "Authorization: Bearer $T" "$A")"
echo "issued token for wget used on curl repo: $(code -H "Authorization: Bearer $T" https://ghcr.io/v2/homebrew/core/curl/tags/list)"
echo "start blob upload with issued token (write): $(code -X POST -H "Authorization: Bearer $T" https://ghcr.io/v2/homebrew/core/wget/blobs/uploads/)"
echo "start blob upload with QQ== (write): $(code -X POST -H 'Authorization: Bearer QQ==' https://ghcr.io/v2/homebrew/core/wget/blobs/uploads/)"
echo "token GET with bogus Basic creds: $(code -u x:y 'https://ghcr.io/token?service=ghcr.io&scope=repository:homebrew/core/wget:pull')"
echo "token GET push scope, anonymous:"
curl -s 'https://ghcr.io/token?service=ghcr.io&scope=repository:homebrew/core/wget:pull,push' | head -c 200; echo
echo "token POST oauth2 password grant, bogus creds:"
curl -s -w ' [%{http_code}]' -X POST --data 'grant_type=password&username=x&password=y&service=ghcr.io&scope=repository:homebrew/core/wget:pull&client_id=x22' https://ghcr.io/token | head -c 300; echo
echo "token POST oauth2 refresh_token grant, bogus:"
curl -s -w ' [%{http_code}]' -X POST --data 'grant_type=refresh_token&refresh_token=y&service=ghcr.io&scope=repository:homebrew/core/wget:pull&client_id=x22' https://ghcr.io/token | head -c 300; echo
