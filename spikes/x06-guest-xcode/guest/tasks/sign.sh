# X06 task: code signing to run locally, as the project user, after mac-build.
# 1. the ad-hoc signature Xcode's "Sign to Run Locally" made;
# 2. a self-signed code signing identity in a keychain in the user's home,
#    created without a GUI, used by codesign. Throwaway.
cd ~/work || exit 1
app=~/work/dd/Build/Products/Debug/MacApp.app
codesign -dv --verbose=2 "$app" 2>&1 | grep -E "Signature|Authority|TeamIdentifier|flags|Identifier"
codesign --verify --strict --verbose=2 "$app" 2>&1; echo "RESULT adhoc verify exit=$?"
codesign -d --entitlements - --xml "$app" 2>/dev/null | plutil -p - 2>&1 | head -6

# The app's binary, started directly (no LaunchServices), for 5 seconds.
"$app/Contents/MacOS/MacApp" > ~/work/run-app.log 2>&1 &
p=$!
sleep 5
if kill -0 $p 2>/dev/null; then echo "RESULT app binary still running after 5s"; kill $p; else wait $p; echo "RESULT app binary exited $?"; fi
head -5 ~/work/run-app.log
open "$app" 2>&1; echo "RESULT open exit=$?"

# A self-signed identity in a keychain the user creates, without a GUI.
kc=~/Library/Keychains/x06-sign.keychain-db
pw=$(openssl rand -hex 12)
rm -f "$kc"
security create-keychain -p "$pw" "$kc" 2>&1; echo "create-keychain exit=$?"
security unlock-keychain -p "$pw" "$kc" 2>&1; echo "unlock exit=$?"
security set-keychain-settings "$kc"
d=$(mktemp -d)
cat > "$d/ext.cnf" <<'EOF'
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = x06 local signing
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout "$d/k.pem" -out "$d/c.pem" -config "$d/ext.cnf" 2>/dev/null
openssl pkcs12 -export -inkey "$d/k.pem" -in "$d/c.pem" -out "$d/id.p12" -passout pass:x06 -legacy 2>/dev/null ||
  openssl pkcs12 -export -inkey "$d/k.pem" -in "$d/c.pem" -out "$d/id.p12" -passout pass:x06
security import "$d/id.p12" -k "$kc" -P x06 -T /usr/bin/codesign 2>&1; echo "import exit=$?"
security set-key-partition-list -S apple-tool:,apple: -s -k "$pw" "$kc" >/dev/null 2>&1; echo "partition-list exit=$?"
security find-identity -v -p codesigning "$kc" 2>&1
cp -R "$app" "$d/MacApp.app"
codesign -f -s "x06 local signing" --keychain "$kc" "$d/MacApp.app" 2>&1; echo "RESULT self-signed codesign exit=$?"
codesign -dv --verbose=2 "$d/MacApp.app" 2>&1 | grep -E "Authority|Signature"
"$d/MacApp.app/Contents/MacOS/MacApp" > ~/work/run-app2.log 2>&1 &
p=$!
sleep 5
if kill -0 $p 2>/dev/null; then echo "RESULT self-signed binary still running after 5s"; kill $p; else wait $p; echo "RESULT self-signed binary exited $?"; fi
head -5 ~/work/run-app2.log
security delete-keychain "$kc"
rm -rf "$d"
