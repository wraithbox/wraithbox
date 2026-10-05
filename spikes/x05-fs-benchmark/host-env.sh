#!/bin/sh
# X05-fs-benchmark spike: record the host the baseline ran on.
sw_vers
sysctl -n machdep.cpu.brand_string hw.ncpu hw.perflevel0.physicalcpu hw.perflevel1.physicalcpu hw.memsize
fdesetup status
systemextensionsctl list
uptime
