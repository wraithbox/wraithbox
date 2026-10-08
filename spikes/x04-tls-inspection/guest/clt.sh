# X20: install the Xcode command line tools as root, as #150 puts them in the
# base image. Uses Apple's software update servers (the spike guest has NAT).
set -eu
t0=$(date +%s)
touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
label=$(softwareupdate -l 2>/dev/null | sed -n 's/^\* Label: \(Command Line Tools.*\)$/\1/p' | sort -V | tail -1)
echo "label: $label"
softwareupdate -i "$label" --verbose 2>&1 | tail -5
rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
xcode-select -p
pkgutil --pkg-info com.apple.pkg.CLTools_Executables | head -3
du -sh /Library/Developer/CommandLineTools
echo "clt seconds: $(( $(date +%s) - t0 ))"
