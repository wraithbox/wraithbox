# X04: supported ways to trust the CA without a user to confirm, after
# `security add-trusted-cert -d` and `security authorizationdb write` were
# refused. As root. Throwaway.
#   1. user domain trust settings for the project user, set as that user
#   2. a configuration profile with the CA, through the profiles tool
set -u
X=/private/tmp/x04
CA=${CA:-ca1}
P=$X/state/$CA.pem
U=wbp-a
echo "== rule com.apple.trust-settings.user"
security authorizationdb read com.apple.trust-settings.user 2>&1 | plutil -p - 2>/dev/null | grep -vE 'created|modified|version|comment|default-' | head -12
echo "== 1. user domain, as $U, with a keychain of its own"
K=/Users/$U/Library/Keychains/x04.keychain-db
sudo -u $U mkdir -p /Users/$U/Library/Keychains
sudo -u $U security create-keychain -p '' $K 2>&1 | tail -1
sudo -u $U security add-trusted-cert -r trustRoot -k $K "$P" 2>&1 | tail -2
echo "exit $?"
sudo -u $U security dump-trust-settings 2>&1 | head -4
echo "verify as $U: $(sudo -u $U security verify-cert -c "$P" -p ssl 2>&1 | tail -1)"
echo "== 2. configuration profile"
B64=$(base64 -i "$P" | tr -d '\n')
cat > $X/ca.mobileconfig <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>PayloadContent</key><array><dict>
<key>PayloadType</key><string>com.apple.security.root</string>
<key>PayloadIdentifier</key><string>org.wraithbox.x04.ca</string>
<key>PayloadUUID</key><string>6B1A6C3E-2F4B-4C1D-9E7A-1F0C3D2B4A01</string>
<key>PayloadVersion</key><integer>1</integer>
<key>PayloadContent</key><data>$B64</data>
</dict></array>
<key>PayloadDisplayName</key><string>X04 spike CA</string>
<key>PayloadIdentifier</key><string>org.wraithbox.x04</string>
<key>PayloadScope</key><string>System</string>
<key>PayloadType</key><string>Configuration</string>
<key>PayloadUUID</key><string>6B1A6C3E-2F4B-4C1D-9E7A-1F0C3D2B4A00</string>
<key>PayloadVersion</key><integer>1</integer>
</dict></plist>
EOF
profiles install -type configuration -path $X/ca.mobileconfig 2>&1 | tail -3
echo "exit $?"
profiles list 2>&1 | tail -3
echo "verify as root: $(security verify-cert -c "$P" -p ssl 2>&1 | tail -1)"
