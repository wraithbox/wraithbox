# X08 boot 1: which attribute makes opendirectoryd refuse a GeneratedUID
# change, and does setting it first, then the rest, stick. Throwaway.
set -u
try() {
  old=$(dscl . -read /Users/wbpf GeneratedUID | awk '{print $2}')
  new=$(uuidgen)
  if dscl . -change /Users/wbpf GeneratedUID "$old" "$new" 2>/dev/null; then r=ok; else r=REFUSED; fi
  echo "after $1: change $r"
}
dscl . -create /Users/wbpf
try record
dscl . -create /Users/wbpf UniqueID 696; try UniqueID
dscl . -create /Users/wbpf PrimaryGroupID 20; try PrimaryGroupID
dscl . -create /Users/wbpf NFSHomeDirectory /var/empty; try NFSHomeDirectory
dscl . -create /Users/wbpf UserShell /bin/zsh; try UserShell
dscl . -create /Users/wbpf RealName "x08 f"; try RealName
dscl . -create /Users/wbpf Password '*'; try Password
dscl . -create /Users/wbpf IsHidden 1; try IsHidden

echo "GUID first, then the rest:"
g=22222222-0000-4000-8000-000000000007
dscl . -create /Users/wbpg GeneratedUID $g
for kv in "UniqueID 697" "PrimaryGroupID 20" "NFSHomeDirectory /var/empty" "UserShell /bin/zsh" "Password *" "IsHidden 1"; do
  set -- $kv
  dscl . -create /Users/wbpg "$1" "$2"
done
dscacheutil -flushcache
echo "wbpg: $(dscl . -read /Users/wbpg GeneratedUID) dsmemberutil $(dsmemberutil getuuid -U wbpg) id $(id wbpg)"
echo "delete a project user record (I138):"
dscl . -delete /Users/wbpg 2>&1 && echo deleted
dscl . -read /Users/wbpg UniqueID 2>&1 | head -1
true
