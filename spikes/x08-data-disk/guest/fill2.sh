# X08 cycle 2: fill each project home as its user ($U1, $U2 under $H), as
# b1-07 and b1-08 did in cycle 1, with the session link named after the
# home path. Then plant the euid probe setuid root in $U2's home. The probe
# was put at /var/root/euid first. Throwaway.
set -u
cat > /tmp/x08-fill-user.sh <<'EOF'
set -u
n=$(id -un); cd "$HOME" || exit 1
umask 022
mkdir -p .claude/projects/wb-project sessions/s1
printf '{"theme":"dark","x08":"%s"}\n' "$n" > .claude/settings.json
printf '{"type":"user","message":"hello from %s"}\n' "$n" > .claude/projects/wb-project/c1.jsonl
link=$(echo "$HOME/sessions/s1" | sed 's/[^A-Za-z0-9]/-/g')
ln -s ../../.claude/projects/wb-project ".claude/projects/$link"
printf '{"numStartups":3,"projects":{"%s/sessions/s1":{"allowedTools":["Bash(git status)"]}}}\n' "$HOME" > .claude.json
chmod 600 .claude.json .claude/settings.json
echo secret-ish > m600; chmod 600 m600
echo public > m644; chmod 644 m644
printf '#!/bin/sh\necho run\n' > m755; chmod 755 m755
echo ro > m400; chmod 400 m400
mkdir d750; chmod 750 d750
xattr -w org.wraithbox.x08 "xattr-$n" m644
xattr -w com.apple.quarantine "0081;00000000;x08;" m755
echo acl > acl-file
chmod +a "group:$n allow read" acl-file
echo acl2 > acl-deny
chmod +a "user:$n deny write,append,writeattr,writeextattr,delete" acl-deny
ln -s m644 link-to-m644
echo flagged > uchg-file; chflags uchg uchg-file
dd if=/dev/urandom of=big.bin bs=1m count=64 2>/dev/null
security create-keychain -p "kc-$n" "$HOME/Library/Keychains/x08.keychain-db"
security add-generic-password -a "$n" -s x08-item -w "item-$n" "$HOME/Library/Keychains/x08.keychain-db"
echo "$n keychain item: $(security find-generic-password -s x08-item -w "$HOME/Library/Keychains/x08.keychain-db")"
echo "$n append to acl-deny: $( (echo x >> acl-deny) 2>/dev/null && echo ALLOWED || echo refused)"
EOF
chmod 644 /tmp/x08-fill-user.sh
for n in $U1 $U2; do
  sudo -u $n -H /bin/sh /tmp/x08-fill-user.sh 2>&1
done
cp /var/root/euid $H/$U2/suid-euid
chown root:wheel $H/$U2/suid-euid
chmod 4755 $H/$U2/suid-euid
ls -l $H/$U2/suid-euid
sync
