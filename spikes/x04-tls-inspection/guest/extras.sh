# X04: two follow-up checks, as root, with the trust variables of
# /etc/wraithbox/env (ca1 + ca2, relay signing with ca2). Throwaway.
#   1. SwiftPM's own downloads (a binary target), which don't go through git.
#   2. A pass-mode host with SSL_CERT_FILE holding only the Wraith Box CAs,
#      against the bundle with the public roots.
set -u
X=/private/tmp/x04
P=wbp-a
HP=/Users/$P
VARS=$(sed 's/^\([A-Z_]*\)=\(.*\)$/\1="\2"/' /etc/wraithbox/env | tr '\n' ' ')
BASEENV="HOME=$HP USER=$P LOGNAME=$P LANG=en_US.UTF-8 TMPDIR=/private/tmp/ PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:$HP/.local/bin"
asP() { eval "sudo -u $P env -i $BASEENV $VARS $1"; }
W=$(mktemp -d /private/tmp/x04b.XXXXXX); mkdir -p $W/Sources/X04B; chown -R $P $W
cat > $W/Package.swift <<'EOF'
// swift-tools-version: 6.0
import PackageDescription
let package = Package(
    name: "X04B",
    targets: [
        .binaryTarget(name: "Nothing", url: "https://example.com/x04/Nothing.xcframework.zip",
                      checksum: "0000000000000000000000000000000000000000000000000000000000000000"),
        .executableTarget(name: "X04B", dependencies: ["Nothing"]),
    ]
)
EOF
echo 'print(1)' > $W/Sources/X04B/main.swift
chown -R $P $W
before=$(wc -l < $X/proxy.jsonl)
echo "== 1. swift package resolve, binary target on example.com (inspected)"
asP "/bin/sh -c 'cd $W && swift package resolve --scratch-path $W/s --cache-path $W/c 2>&1 | tail -4'"
tail -n +$((before + 1)) $X/proxy.jsonl | grep -v cloudkit
echo "== 2. pass-mode example.com"
echo example.com > $X/state/pass
for f in /etc/wraithbox/ca.pem /etc/wraithbox/ca-bundle.pem; do
  echo "-- SSL_CERT_FILE=$f"
  sudo -u $P env -i $BASEENV SSL_CERT_FILE=$f /opt/homebrew/opt/curl/bin/curl -sS -o /dev/null -w '%{http_code}\n' https://example.com/ 2>&1 | tail -1
  sudo -u $P env -i $BASEENV SSL_CERT_FILE=$f $X/bin/x04-goclient https://example.com/ 2>&1 | tail -1
  sudo -u $P env -i $BASEENV SSL_CERT_FILE=$f python3.14 $X/clients/loop.py urllib https://example.com/ 2>&1 | tail -1
done
: > $X/state/pass
tail -n 7 $X/proxy.jsonl | grep -c '"mode":"pass"'
rm -rf $W
