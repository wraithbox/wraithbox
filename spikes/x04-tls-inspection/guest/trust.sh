# X04: install or remove the spike CAs in the guest's trust, as root, the way
# wb-guestd would (S09-policy-credentials-audit, "Guest trust"). Throwaway.
#
#   MODE=sys-add CA=ca1     system keychain, trusted for every user
#   MODE=sys-remove CA=ca1
#   MODE=vars CAS="ca1 ca2" trust variable files and /etc/wraithbox/env
#   MODE=vars-off           remove /etc/wraithbox/env (the files stay)
set -u
X=/private/tmp/x04
W=/etc/wraithbox
SK=/Library/Keychains/System.keychain
case "$MODE" in
sys-add)
  P=$X/state/$CA.pem
  t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
  security add-trusted-cert -d -r trustRoot -k $SK "$P"
  rc=$?
  echo "add-trusted-cert exit $rc"
  if [ $rc -ne 0 ]; then
    # Without a user to confirm, trust settings for admin need this right.
    security authorizationdb read com.apple.trust-settings.admin > $X/authdb-trust-admin.plist 2>/dev/null
    echo "authorizationdb rule before:"; plutil -p $X/authdb-trust-admin.plist | grep -E '"(rule|class|shared|authenticate-user)"'
    security authorizationdb write com.apple.trust-settings.admin allow
    security add-trusted-cert -d -r trustRoot -k $SK "$P"
    echo "add-trusted-cert with the right allowed: exit $?"
    security authorizationdb write com.apple.trust-settings.admin < $X/authdb-trust-admin.plist
    echo "authorizationdb rule restored:"; security authorizationdb read com.apple.trust-settings.admin 2>/dev/null | plutil -p - | grep -E '"(rule|class)"'
  fi
  t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
  echo "install seconds: $(echo "$t1 - $t0" | bc)"
  security dump-trust-settings -d 2>&1 | grep -A3 "X04 spike $CA" | head -4
  security verify-cert -c "$P" -p ssl 2>&1 | tail -1
  ;;
sys-remove)
  P=$X/state/$CA.pem
  security remove-trusted-cert -d "$P"
  echo "remove-trusted-cert exit $?"
  if [ $? -ne 0 ] || security dump-trust-settings -d 2>/dev/null | grep -q "X04 spike $CA"; then
    security authorizationdb read com.apple.trust-settings.admin > $X/authdb-trust-admin.plist 2>/dev/null
    security authorizationdb write com.apple.trust-settings.admin allow
    security remove-trusted-cert -d "$P"
    echo "remove-trusted-cert with the right allowed: exit $?"
    security authorizationdb write com.apple.trust-settings.admin < $X/authdb-trust-admin.plist
  fi
  security delete-certificate -c "Wraith Box X04 spike $CA" $SK
  echo "delete-certificate exit $?"
  security dump-trust-settings -d 2>&1 | grep -c "X04 spike" || true
  ;;
vars)
  mkdir -p $W
  : > $W/ca.pem.new
  for c in $CAS; do cat $X/state/$c.pem >> $W/ca.pem.new; done
  # Replacing variables need the public roots too: pass-mode hosts, and
  # any host not inspected, present public certificates.
  cat /etc/ssl/cert.pem $W/ca.pem.new > $W/ca-bundle.pem.new
  mv $W/ca.pem.new $W/ca.pem
  mv $W/ca-bundle.pem.new $W/ca-bundle.pem
  JH=/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home
  rm -f $W/ca-bundle.p12
  $JH/bin/keytool -importkeystore -noprompt -srckeystore $JH/lib/security/cacerts -srcstorepass changeit \
    -destkeystore $W/ca-bundle.p12 -deststoretype pkcs12 -deststorepass changeit > /dev/null 2>&1
  for c in $CAS; do
    $JH/bin/keytool -importcert -noprompt -alias "wraithbox-$c" -file $X/state/$c.pem \
      -keystore $W/ca-bundle.p12 -storepass changeit > /dev/null 2>&1
  done
  chmod 644 $W/*
  cat > $W/env.all <<EOF
SSL_CERT_FILE=$W/ca-bundle.pem
CURL_CA_BUNDLE=$W/ca-bundle.pem
REQUESTS_CA_BUNDLE=$W/ca-bundle.pem
PIP_CERT=$W/ca-bundle.pem
GIT_SSL_CAINFO=$W/ca-bundle.pem
CARGO_HTTP_CAINFO=$W/ca-bundle.pem
NODE_EXTRA_CA_CERTS=$W/ca.pem
JAVA_TOOL_OPTIONS=-Djavax.net.ssl.trustStore=$W/ca-bundle.p12 -Djavax.net.ssl.trustStorePassword=changeit
EOF
  cp $W/env.all $W/env
  echo "ca.pem: $(grep -c BEGIN $W/ca.pem) certs; ca-bundle.pem: $(grep -c BEGIN $W/ca-bundle.pem) certs; p12: $($JH/bin/keytool -list -keystore $W/ca-bundle.p12 -storepass changeit 2>/dev/null | grep -c trustedCertEntry) entries"
  cat $W/env
  ;;
vars-off)
  rm -f $W/env
  echo "vars off"
  ;;
esac
