# X08 boot 1: fill each project home as its user. Claude Code state (the
# layout of S06-vm-lifecycle, not a real claude, which the image lacks),
# files of several modes, xattrs, ACLs that name the user and its group,
# a symbolic link, file flags, a keychain with one item, and a large file.
# Then plant a setuid-root binary in wbp2's home as root. Throwaway.
set -u
H=/Volumes/wbhomes
echo "claude in the image: $(command -v claude || echo none)"
cat > /tmp/x08-fill-user.sh <<'EOF'
set -u
n=$(id -un); cd "$HOME" || exit 1
umask 022
mkdir -p .claude/projects/wb-project sessions/s1
printf '{"theme":"dark","x08":"%s"}\n' "$n" > .claude/settings.json
printf '{"type":"user","message":"hello from %s"}\n' "$n" > .claude/projects/wb-project/c1.jsonl
ln -s ../../.claude/projects/wb-project .claude/projects/-Volumes-wbhomes-$n-sessions-s1
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
chmod +a "user:$n deny write" acl-file
chmod +a "group:$n allow read" acl-file
ln -s m644 link-to-m644
echo flagged > uchg-file; chflags uchg uchg-file
dd if=/dev/urandom of=big.bin bs=1m count=64 2>/dev/null
security create-keychain -p "kc-$n" "$HOME/Library/Keychains/x08.keychain-db"
security add-generic-password -a "$n" -s x08-item -w "item-$n" "$HOME/Library/Keychains/x08.keychain-db"
security find-generic-password -s x08-item -w "$HOME/Library/Keychains/x08.keychain-db"
echo "write to the deny-write file as $n: $(echo x >> acl-file 2>&1 && echo ALLOWED || echo refused)"
ls -la "$HOME/Library/Keychains"
EOF
chmod 644 /tmp/x08-fill-user.sh
for n in wbp1 wbp2; do
  echo "== $n"
  sudo -u $n -H /bin/sh /tmp/x08-fill-user.sh 2>&1
done
cp /usr/bin/id $H/wbp2/suid-id
chown root:wheel $H/wbp2/suid-id
chmod 4755 $H/wbp2/suid-id
ls -l $H/wbp2/suid-id
sync
