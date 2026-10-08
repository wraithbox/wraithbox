# X04: start the throwaway inspecting relay as `nobody` and redirect the
# guest's outgoing TCP 443 to it with pf, as root. Every process except the
# relay's own is redirected, so no client gets proxy settings
# (FR08-no-proxy-config). QUIC (UDP 443) is dropped and IPv6 TCP 443 refused,
# so clients fall back to IPv4 TCP. The CAs are made on the first run.
# Throwaway: the product does this on the host (S07-egress-gateway).
set -eu
X=/private/tmp/x04
S="/Volumes/My Shared Files"
mkdir -p $X/state $X/bin
cp "$S/x04-proxy" "$S/x04-goclient" "$S/x04-swiftclient" $X/bin/
cp -R "$S/clients" $X/
chmod -R a+rX $X
if [ ! -f $X/state/ca1.pem ]; then
  $X/bin/x04-proxy gen-ca $X/state ca1 60
  $X/bin/x04-proxy gen-ca $X/state ca2 60
  echo ca1 > $X/state/current
  : > $X/state/pass
fi
chown -R nobody $X/state
touch $X/proxy.jsonl && chown nobody $X/proxy.jsonl
launchctl remove org.wraithbox.x04-proxy 2>/dev/null || true
sleep 1
# x17-guestd's scripts can't leave a background process, so launchd runs it.
launchctl submit -l org.wraithbox.x04-proxy -o $X/proxy.err -e $X/proxy.err -- \
  /usr/bin/sudo -u nobody $X/bin/x04-proxy serve 127.0.0.1:8443 $X/state $X/proxy.jsonl
sleep 2
cat > $X/pf.conf <<'EOF'
rdr pass on lo0 inet proto tcp from any to any port 443 -> 127.0.0.1 port 8443
block drop out quick on en0 proto udp from any to any port 443
block return out quick on en0 inet6 proto tcp from any to any port 443
pass out quick on en0 route-to (lo0 127.0.0.1) inet proto tcp from any to any port 443 user != nobody keep state
pass all
EOF
pfctl -E -f $X/pf.conf 2>&1 | grep -v ALTQ || true
pfctl -s nat 2>/dev/null
pfctl -s rules 2>/dev/null
pgrep -fl 'x04-proxy serve'
cat $X/proxy.err
for c in ca1 ca2; do openssl x509 -in $X/state/$c.pem -noout -subject -fingerprint -sha256 -enddate; done
