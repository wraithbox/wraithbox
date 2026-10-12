# X08 boot 3: take the TCC-refused USB volume off the home mount point.
# Throwaway.
diskutil unmount /private/var/wbhomes
mount | grep -E 'wbhomes|WBPass|WBHomes'
true
