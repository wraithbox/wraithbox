#!/bin/sh
# run the matrix with one trust variable at a time, for the clients that
# SSL_CERT_FILE alone didn't cover
R=spikes/x04-tls-inspection/r.sh
M=spikes/x04-tls-inspection/guest/matrix.sh
one() {
  $R run $M 3600 COND=only-$1 ONLYVAR=$1 "ONLY=$2" > /dev/null
  spikes/x04-tls-inspection/fetch.sh /private/tmp/x04/matrix-only-$1.jsonl matrix-only-$1.jsonl
  python3 spikes/x04-tls-inspection/show.py spikes/x04-tls-inspection/results/matrix-only-$1.jsonl
}
one GIT_SSL_CAINFO "git-apple git-brew swiftpm-cli xcodebuild-spm xcodebuild-spm-system-git cargo"
one CARGO_HTTP_CAINFO "cargo"
one CURL_CA_BUNDLE "curl-apple curl-brew git-apple git-brew python-requests pip cargo"
one REQUESTS_CA_BUNDLE "python-requests pip"
one PIP_CERT "pip"
one NODE_EXTRA_CA_CERTS "node npm claude"
one JAVA_TOOL_OPTIONS "java"
