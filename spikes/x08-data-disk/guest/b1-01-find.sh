# X08 boot 1: which disks the guest sees, before anything is formatted. Throwaway.
sw_vers
diskutil list
for d in disk0 disk1 disk2 disk3 disk4 disk5 disk6; do
  diskutil info $d 2>/dev/null | grep -E 'Device Identifier|Device / Media Name|Disk Size|Protocol|Internal|Removable|Ejectable|Content \(IOContent\)|Virtual|Solid State'
  echo
done
mount
dscl . -list /Users UniqueID | awk '$2>=500'
sysadminctl -secureTokenStatus wbprov 2>&1
fdesetup status
true
