# X08: the behavior checks, run on every boot after the users exist.
# Throwaway.
set -u
H=/Volumes/wbhomes
echo "mount: $(mount | grep " $H " || echo NOT MOUNTED)"
for n in wbp1 wbp2; do
  echo "== $n: $(id $n 2>&1)"
  sudo -u $n -H /bin/sh -c '
    n=$(id -un); cd "$HOME" || { echo "cd home failed"; exit 1; }
    echo "  HOME=$HOME"
    echo "  claude settings: $(cat .claude/settings.json 2>&1)"
    echo "  claude.json: $(cat .claude.json 2>&1 | cut -c1-80)"
    echo "  conversation via the session link: $(cat .claude/projects/-Volumes-wbhomes-$n-sessions-s1/c1.jsonl 2>&1)"
    echo "  write in home: $(echo x > w-test 2>&1 && echo ok && rm -f w-test)"
    echo "  append to acl-deny: $( (echo x >> acl-deny) 2>/dev/null && echo ALLOWED || echo refused)"
    echo "  write m400: $( (echo x >> m400) 2>/dev/null && echo ALLOWED || echo refused)"
    echo "  rm uchg-file: $(rm -f uchg-file 2>/dev/null; [ -e uchg-file ] && echo refused || echo REMOVED)"
    echo "  run m755: $(./m755 2>&1)"
    kc="$HOME/Library/Keychains/x08.keychain-db"
    echo "  keychain unlock: $(security unlock-keychain -p "kc-$n" "$kc" 2>&1 && echo ok)"
    echo "  keychain item: $(security find-generic-password -s x08-item -w "$kc" 2>&1)"
    echo "  keychain new item: $(security add-generic-password -U -a "$n" -s x08-item2 -w new "$kc" 2>&1 && echo ok)"
    echo "  login keychain: $(security login-keychain 2>&1)"
  ' 2>&1
done
echo "== cross-user, as wbp2 against wbp1 and as wbp1 against wbp2"
sudo -u wbp2 /bin/sh -c '
  echo "  wbp2 ls wbp1 home: $(ls /Volumes/wbhomes/wbp1 2>&1 | head -1)"
  echo "  wbp2 read wbp1 .claude.json: $(cat /Volumes/wbhomes/wbp1/.claude.json 2>&1 | head -1)"
  echo "  wbp2 read wbp1 keychain: $(head -c 4 /Volumes/wbhomes/wbp1/Library/Keychains/x08.keychain-db 2>&1 | head -1)"
  echo "  wbp2 write in data volume root: $( (echo x > /Volumes/wbhomes/x) 2>&1 | head -1)"' 2>&1
sudo -u wbp1 /bin/sh -c '
  echo "  wbp1 ls wbp2 home: $(ls /Volumes/wbhomes/wbp2 2>&1 | head -1)"
  echo "  wbp1 run wbp2 suid-euid: $(/Volumes/wbhomes/wbp2/suid-euid 2>&1 | head -1)"' 2>&1
echo "== setuid binary, as wbp2"
ls -l $H/wbp2/suid-euid
sudo -u wbp2 /bin/sh -c 'echo "  euid via suid-id: $(/Volumes/wbhomes/wbp2/suid-euid)"' 2>&1
true
