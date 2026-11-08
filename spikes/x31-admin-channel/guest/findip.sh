#!/bin/sh
# Print the guest's address: the last DHCP lease for MAC $1 (as aa:bb:..).
leasemac=$(echo "$1" | awk -F: '{for(i=1;i<=NF;i++){sub(/^0/,"",$i); printf "%s%s",$i,(i<NF?":":"")}}')
awk -v m="1,$leasemac" '/ip_address=/{split($0,a,"=");ipa=a[2]} /hw_address=/{split($0,b,"=");if(b[2]==m){print ipa}}' /var/db/dhcpd_leases 2>/dev/null | tail -1
