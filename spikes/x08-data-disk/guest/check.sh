# X08: the behavior checks, run on every boot after the users exist.
# $H, $U1 and $U2 come from the host-state header (defaults: cycle 1).
# Throwaway.
set -u
H=${H:-/Volumes/wbhomes}; U1=${U1:-wbp1}; U2=${U2:-wbp2}
export H U1 U2
echo "mount: $(mount | grep " $H " || echo NOT MOUNTED)"
for n in $U1 $U2; do
  echo "== $n: $(id $n 2>&1)"
  echo "   GeneratedUID $(dsmemberutil getuuid -U $n 2>&1)"
  sudo -u $n -H /bin/sh -c '
    n=$(id -un); cd "$HOME" || { echo "cd home failed"; exit 1; }
    link=$(echo "$HOME/sessions/s1" | sed "s/[^A-Za-z0-9]/-/g")
    echo "  HOME=$HOME"
    echo "  claude settings: $(cat .claude/settings.json 2>&1)"
    echo "  claude.json: $(cat .claude.json 2>&1 | cut -c1-80)"
    echo "  conversation via the session link: $(cat .claude/projects/$link/c1.jsonl 2>&1)"
    echo "  write in home: $( (echo x > w-test) 2>&1 && echo ok && rm -f w-test)"
    echo "  write in .claude: $( (echo x > .claude/w-test) 2>&1 && echo ok && rm -f .claude/w-test)"
    echo "  append to acl-deny (ACL denies the user): $( (echo x >> acl-deny) 2>/dev/null && echo ALLOWED || echo refused)"
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
echo "== cross-user"
sudo -u $U2 env H=$H U1=$U1 U2=$U2 /bin/sh -c '
  echo "  $U2 ls $U1 home: $(ls $H/$U1 2>&1 | head -1)"
  echo "  $U2 read $U1 .claude.json: $(cat $H/$U1/.claude.json 2>&1 | head -1)"
  echo "  $U2 read $U1 keychain: $(head -c 4 $H/$U1/Library/Keychains/x08.keychain-db 2>&1 | head -1)"
  echo "  $U2 write in data volume root: $( (echo x > $H/x) 2>&1 | head -1)"' 2>&1
sudo -u $U1 env H=$H U1=$U1 U2=$U2 /bin/sh -c '
  echo "  $U1 ls $U2 home: $(ls $H/$U2 2>&1 | head -1)"
  echo "  $U1 run $U2 suid-euid: $($H/$U2/suid-euid 2>&1 | head -1)"' 2>&1
echo "== setuid binary, as $U2"
ls -l $H/$U2/suid-euid
sudo -u $U2 env H=$H U1=$U1 U2=$U2 /bin/sh -c 'echo "  euid via suid-euid: $($H/$U2/suid-euid)"' 2>&1
true
