# X17-image-build: show that the kept provisioning user can't log in. Run as
# root by x17-guestd; mkreq.py prepends P='<old provisioning password>'.
# Throwaway.
u=$(dscl . -list /Users UniqueID | awk '$2>=500 {print $1}' | head -1)
echo "user: $u"
dscl . -read /Users/"$u" accountPolicyData | tr -d '\t' | grep -A1 isDisabled
pwpolicy -u "$u" -authentication-allowed 2>&1
dscl . -authonly "$u" "$P" >/dev/null 2>&1 && echo "old password: accepted" || echo "old password: refused"
id "$u"
diskutil apfs listCryptoUsers / | grep -E "\+--|Volume Owner"
dscl . -read /Users/"$u" GeneratedUID
