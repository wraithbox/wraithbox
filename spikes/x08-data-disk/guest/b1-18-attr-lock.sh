# X08 boot 1: dscl refused to change wbp1's NFSHomeDirectory. Which changes
# does opendirectoryd still take from this root daemon on a complete user
# record? Throwaway.
set -u
r() { dscl . -read /Users/wbp1 "$1" 2>&1 | tr '\n' ' '; echo; }
echo "RealName -create: $(dscl . -create /Users/wbp1 RealName 'x08 wbp1 renamed' 2>&1 | tail -1) -> $(r RealName)"
echo "UserShell -create: $(dscl . -create /Users/wbp1 UserShell /bin/sh 2>&1 | tail -1) -> $(r UserShell)"
echo "NFSHomeDirectory -delete: $(dscl . -delete /Users/wbp1 NFSHomeDirectory 2>&1 | tail -1) -> $(r NFSHomeDirectory)"
echo "NFSHomeDirectory -create: $(dscl . -create /Users/wbp1 NFSHomeDirectory /private/var/wbhomes/wbp1 2>&1 | tail -1) -> $(r NFSHomeDirectory)"
echo "sysadminctl? (no home flag for an existing user):"; sysadminctl 2>&1 | grep -iE 'home|change' | head -5
echo "a new scratch record, complete, then change the home:"
dscl . -create /Users/wbph GeneratedUID $(uuidgen); dscl . -create /Users/wbph UniqueID 698; dscl . -create /Users/wbph PrimaryGroupID 20
echo "  NFSHomeDirectory set after UniqueID+PrimaryGroupID: $(dscl . -create /Users/wbph NFSHomeDirectory /var/empty 2>&1 | tail -1) -> $(dscl . -read /Users/wbph NFSHomeDirectory 2>&1)"
echo "  and changed: $(dscl . -create /Users/wbph NFSHomeDirectory /private/var/empty2 2>&1 | tail -1) -> $(dscl . -read /Users/wbph NFSHomeDirectory 2>&1)"
echo "TCC / FDA of this process: $(ps -o pid,command -p $PPID | tail -1)"
true
