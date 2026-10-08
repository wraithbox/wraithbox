# X04: test fixtures for the project user, as root: a venv with requests and
# a SwiftPM package with one dependency. The Python hosts are put in pass
# mode for this step only, since nothing trusts the CA yet. Throwaway.
set -eu
X=/private/tmp/x04
P=wbp-a
printf 'pypi.org\nfiles.pythonhosted.org\n' > $X/state/pass
[ -x /Users/$P/venv/bin/pip ] || sudo -u $P -i /bin/sh -c '/opt/homebrew/bin/python3.14 -m venv ~/venv && ~/venv/bin/pip install -q requests && ~/venv/bin/pip list 2>/dev/null | grep -iE "requests|certifi|pip "'
: > $X/state/pass
D=/Users/$P/pkg
mkdir -p $D/Sources/X04
cat > $D/Package.swift <<'EOF2'
// swift-tools-version: 6.0
import PackageDescription
let package = Package(
    name: "X04",
    dependencies: [.package(url: "https://github.com/apple/swift-argument-parser", from: "1.5.0")],
    targets: [.executableTarget(name: "X04", dependencies: [.product(name: "ArgumentParser", package: "swift-argument-parser")])]
)
EOF2
echo 'print(1)' > $D/Sources/X04/main.swift
chown -R $P:staff $D
ls -lR $D
tail -3 $X/proxy.jsonl
