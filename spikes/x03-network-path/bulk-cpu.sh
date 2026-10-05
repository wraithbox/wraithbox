#!/bin/sh
# X03-network-path spike: one 1 GiB download in the guest, with the CPU time
# that x03-netd and Virtualization's service process used for it.
# Usage: bulk-cpu.sh <scratch dir> [runs] [url]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
runs=${2:-3}
url=${3:-http://bulk.test/1g}
netd=$(pgrep -x x03-netd)
vz=$(pgrep -f 'com.apple.Virtualization.VirtualMachine' | paste -sd, -)
cpu() { ps -o time= -p "$1" | awk -F'[:.]' '{ if (NF==3) print $1*60+$2+$3/100; else print $1*3600+$2*60+$3+$4/100 }' | paste -sd+ - | bc; }
i=1
while [ "$i" -le "$runs" ]; do
  n0=$(cpu "$netd"); v0=$(cpu "$vz")
  out=$("$here/gssh.sh" "$1" "curl -sS -o /dev/null -w '%{size_download} %{time_total} %{speed_download}' $url")
  n1=$(cpu "$netd"); v1=$(cpu "$vz")
  echo "$out" | awk -v nd="$(echo "$n1 - $n0" | bc)" -v vd="$(echo "$v1 - $v0" | bc)" \
    '{ printf "bytes %s seconds %s MBps %.1f netdCPUs %s vzCPUs %s\n", $1, $2, $3/1e6, nd, vd }'
  i=$((i + 1))
done
