# X20, as root: two project users, standard accounts as wb-guestd creates them
# (S06-vm-lifecycle): not in admin or wheel, no sudoers rule, home 700.
set -u
for u in wbp-a wbp-b; do
  if ! id "$u" >/dev/null 2>&1; then
    sysadminctl -addUser "$u" -fullName "x20 $u" -shell /bin/zsh -password "$(openssl rand -hex 16)" -home /Users/"$u" 2>&1 | grep -v -- '-----'
    createhomedir -c -u "$u" 2>&1 | tail -1
  fi
  chmod 700 /Users/"$u"
  id "$u"
  dsmemberutil checkmembership -U "$u" -G admin
  ls -ld /Users/"$u"
done
