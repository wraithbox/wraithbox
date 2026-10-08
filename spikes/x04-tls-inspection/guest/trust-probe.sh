# X04: ways for root (wb-guestd) to make the system trust a CA without a
# user to confirm, since `security add-trusted-cert -d` is refused. As root.
# Throwaway.
set -u
X=/private/tmp/x04
CA=${CA:-ca1}
P=$X/state/$CA.pem
SK=/Library/Keychains/System.keychain
TS="/Library/Security/Trust Settings"
v() { echo "verify-cert: $(security verify-cert -c "$P" -p ssl 2>&1 | tail -1)"; }

echo "== 1. user domain, as the project user (no GUI session)"
sudo -u wbp-a security add-trusted-cert -r trustRoot -k /Users/wbp-a/Library/Keychains/login.keychain-db "$P" 2>&1 | tail -2
echo "exit $?"

echo "== 2. trust-settings-import -d"
cat > $X/empty-ts.plist <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>trustList</key><dict/><key>trustVersion</key><integer>1</integer></dict></plist>
EOF
security trust-settings-import -d $X/empty-ts.plist 2>&1 | tail -1

echo "== 3. the certificate in the System keychain, no trust settings"
security add-certificates -k $SK "$P" 2>&1 | tail -1
security find-certificate -c "Wraith Box X04 spike $CA" $SK > /dev/null && echo "in System keychain"
v

echo "== 4. the admin trust settings file, written by root"
ls -la "$TS" 2>&1
ls -lO "/Library/Security" 2>&1 | head
/usr/bin/python3 - "$P" "$X/Admin.plist" <<'PY'
import datetime, hashlib, plistlib, ssl, subprocess, sys
pem = open(sys.argv[1]).read()
der = ssl.PEM_cert_to_DER_cert(pem)
sha1 = hashlib.sha1(der).hexdigest().upper()
def field(name):
    out = subprocess.run(["openssl", "asn1parse", "-inform", "DER"], input=der, capture_output=True).stdout.decode()
    return out
# Issuer name DER and serial: use the cryptography-free route via openssl x509.
issuer = subprocess.run(["openssl", "x509", "-noout", "-issuer", "-nameopt", "RFC2253"], input=pem.encode(), capture_output=True).stdout
serial_hex = subprocess.run(["openssl", "x509", "-noout", "-serial"], input=pem.encode(), capture_output=True).stdout.decode().strip().split("=")[1]
# Issuer DER: the CA is self-signed, so issuer == subject; take the subject
# Name from the TBSCertificate with a small DER walk.
def tlv(b, i):
    t = b[i]; l = b[i + 1]; j = i + 2
    if l & 0x80:
        n = l & 0x7F; l = int.from_bytes(b[j:j + n], "big"); j += n
    return t, j, j + l
_, s, e = tlv(der, 0)          # Certificate
_, s, e = tlv(der, s)          # TBSCertificate
i = s
t, cs, ce = tlv(der, i)
if t == 0xA0:                  # version
    i = ce
fields = []
while i < e and len(fields) < 6:
    t, cs, ce = tlv(der, i)
    fields.append(der[i:ce]); i = ce
serial_der, sigalg, issuer_der = fields[0], fields[1], fields[2]
serial = bytes.fromhex(serial_hex if len(serial_hex) % 2 == 0 else "0" + serial_hex)
d = {"trustList": {sha1: {"issuerName": issuer_der, "serialNumber": serial,
                          "modDate": datetime.datetime.utcnow(),
                          "trustSettings": []}},
     "trustVersion": 1}
plistlib.dump(d, open(sys.argv[2], "wb"))
print("sha1", sha1, "serial", serial.hex())
PY
mkdir -p "$TS"
cp $X/Admin.plist "$TS/Admin.plist" && echo "wrote $TS/Admin.plist"
ls -la "$TS"
security dump-trust-settings -d 2>&1 | head -8
v
echo "== 4b. after killall trustd"
killall trustd 2>/dev/null; sleep 2
security dump-trust-settings -d 2>&1 | head -8
v
