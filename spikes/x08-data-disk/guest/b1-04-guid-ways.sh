# X08 boot 1: dscl refused to set a user's GeneratedUID after the record
# existed. Try other ways to create a user with a chosen GeneratedUID, each
# on a scratch user. Throwaway.
set -u
show() { echo "  -> $(dscl . -read /Users/$1 GeneratedUID 2>&1 | tr '\n' ' ')"; }

echo "A: dscl -change on an existing record"
dscl . -create /Users/wbpa; dscl . -create /Users/wbpa UniqueID 691
old=$(dscl . -read /Users/wbpa GeneratedUID | awk '{print $2}')
dscl . -change /Users/wbpa GeneratedUID "$old" 11111111-0000-4000-8000-00000000000A 2>&1
show wbpa

echo "B: dscl -delete the attribute, then -create"
dscl . -delete /Users/wbpa GeneratedUID 2>&1
dscl . -create /Users/wbpa GeneratedUID 11111111-0000-4000-8000-00000000000B 2>&1
show wbpa

echo "C: dsimport of a record that carries GeneratedUID"
cat > /tmp/x08-c.txt <<'EOF'
0x0A 0x5C 0x3A 0x2C dsRecTypeStandard:Users 5 dsAttrTypeStandard:RecordName dsAttrTypeStandard:UniqueID dsAttrTypeStandard:PrimaryGroupID dsAttrTypeStandard:GeneratedUID dsAttrTypeStandard:NFSHomeDirectory
wbpc:693:20:11111111-0000-4000-8000-00000000000C:/var/empty
EOF
dsimport /tmp/x08-c.txt /Local/Default I 2>&1 | tail -5
show wbpc

echo "D: dscl -create with GeneratedUID as the first attribute set (record and value in one call?)"
dscl . -create /Users/wbpd GeneratedUID 11111111-0000-4000-8000-00000000000D 2>&1
show wbpd

echo "E: sysadminctl -addUser, then dscl -create GeneratedUID"
sysadminctl -addUser wbpe -UID 695 -shell /usr/bin/false -home /var/empty 2>&1 | grep -v -- '-----' | tail -2
dscl . -create /Users/wbpe GeneratedUID 11111111-0000-4000-8000-00000000000E 2>&1
show wbpe

echo "F: dscl -createpl / -merge"
dscl . -merge /Users/wbpe GeneratedUID 11111111-0000-4000-8000-00000000000F 2>&1
show wbpe

echo "G: is dslocal writable by this root process?"
ls -la /var/db/dslocal/nodes/Default/users/ 2>&1 | head -3
ls -le /var/db/dslocal/nodes/Default/users/wbpa.plist 2>&1
csrutil status
true
