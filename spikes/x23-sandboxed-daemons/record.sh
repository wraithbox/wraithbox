#!/bin/bash
# Re-runs every measurement and writes the output to results/.
cd "$(dirname "$0")" || exit 1
mkdir -p results
{
  sw_vers
  go version
} >results/env.txt
./probe.sh probe-nocgo none profiles/netd.sb >results/probe-unconfined.txt
./probe.sh probe-nocgo pure profiles/deny-all.sb >results/probe-pure-deny-all.txt
./probe.sh probe-nocgo pure profiles/netd.sb >results/probe-pure-netd.txt
./probe.sh probe cgo profiles/netd.sb >results/probe-cgo-netd.txt
./probe.sh probe-nocgo exec profiles/deny-all.sb >results/probe-exec-deny-all.txt
./probe.sh probe-nocgo exec profiles/exec-min.sb >results/probe-exec-min.txt
./probe.sh probe-nocgo pure profiles/broken.sb >results/probe-broken-profile.txt
./probe.sh probe-nocgo pure profiles/proxyd-net.sb >results/probe-proxyd-net.txt
./probe.sh probe-nocgo pure profiles/proxyd-net-tls.sb >results/probe-proxyd-net-tls.txt
./bin/harness -mech pure -netd bin/netd-nocgo -profile profiles/netd.sb >results/netd-pure.txt 2>&1
./bin/harness -mech exec -netd bin/netd-nocgo -profile profiles/netd-exec.sb >results/netd-exec.txt 2>&1
./bench.sh 5 64 >results/bench.txt
./bench2.sh 3 profiles/deny-all.sb profiles/netd.sb >results/bench-ncpu.txt
./hostd.sh pure profiles/hostd.sb /opt/homebrew/bin/git /opt/homebrew >results/hostd-direct-children.txt
./hostd.sh pure profiles/hostd.sb /opt/homebrew/bin/git /opt/homebrew hostd-nocgo -spawner >results/hostd-spawner-brew-git.txt
./hostd.sh pure profiles/hostd.sb /Applications/Xcode.app/Contents/Developer/usr/bin/git /Applications/Xcode.app/Contents/Developer hostd-nocgo -spawner >results/hostd-spawner-xcode-git.txt
./hostd.sh pure profiles/hostd.sb /usr/bin/git /Applications/Xcode.app/Contents/Developer hostd-nocgo -spawner >results/hostd-spawner-usr-bin-git-shim.txt
./denials.sh 600 hostd-nocgo >results/denials-hostd.txt
./denials.sh 600 netd-nocgo >results/denials-netd.txt
