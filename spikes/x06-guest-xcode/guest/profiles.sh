# X06-guest-xcode: run as root. Write the draft Seatbelt profiles to
# /var/db/x06/ (root-owned, readable by everyone):
#   p0.sb  the S13-guest-confinement layer 2 draft without Xcode exceptions:
#          the agent-safehouse v0.12.0 default selection as
#          X27-vsock-confinement rendered it (s2), with HOME_DIR /Users/x06p,
#          plus S13's grants (read and write in the user's home and the
#          worktree /Users/x06p/work, the Xcode bundle readable), and the
#          S13 vsock rule last
#   p1.sb  p0 plus the Xcode exceptions in /var/db/x06/xcode.sb, before the
#          vsock rule, and without the safehouse's (deny process-info-pidinfo)
#   p2.sb  p1 plus /var/db/x06/xcode-simtest.sb, what xcodebuild test on a
#          simulator needed to finish
# The exceptions files come from the host (x06-guestd put). Throwaway.
set -eu
mkdir -p /var/db/x06
s13() {
  cat <<'EOF'

;; X06: S13-guest-confinement layer 2 grants. Reads of the system, Homebrew
;; and Xcode, and of the user's own home; writes in the home, the worktree
;; and temporary directories (the safehouse modules already allow /tmp and
;; /private/var/folders).
(allow file-read* (literal "/") (literal "/Users") (literal "/Applications"))
(allow file-read* (subpath "/Applications/Xcode.app"))
(allow file-read* file-write* (subpath "/Users/x06p"))
EOF
}
vsock() {
  printf '\n;; S13-guest-confinement: no AF_VSOCK for project users\n(deny system-socket (socket-domain AF_VSOCK))\n'
}
sed -e 's|/Users/proj1/work|/Users/x06p/work|g' -e 's|/Users/proj1|/Users/x06p|g' \
  /Volumes/X06XCODE/x06/s2-safehouse-deny-vsock.sb | grep -v 'socket-domain AF_VSOCK' > /var/db/x06/safehouse.sb
{ cat /var/db/x06/safehouse.sb; s13; vsock; } > /var/db/x06/p0.sb
if [ -f /var/db/x06/xcode.sb ]; then
  # The safehouse base denies pidinfo with a rule that a later allow does not
  # undo (X06 ios-test). p1 leaves that one rule out; xcode.sb allows pidinfo.
  { grep -v '^(deny process-info-pidinfo)' /var/db/x06/safehouse.sb; s13; cat /var/db/x06/xcode.sb; vsock; } > /var/db/x06/p1.sb
  { grep -v '^(deny process-info-pidinfo)' /var/db/x06/safehouse.sb; s13; cat /var/db/x06/xcode.sb /var/db/x06/xcode-simtest.sb; vsock; } > /var/db/x06/p2.sb
fi
chmod 644 /var/db/x06/*.sb
grep -n 'define HOME_DIR\|AF_VSOCK' /var/db/x06/p*.sb
wc -l /var/db/x06/*.sb
