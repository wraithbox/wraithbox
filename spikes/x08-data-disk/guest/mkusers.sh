# X08: create the project users from host state ($USERS, $HOMES, prepended
# by the host). Each gets its own group, a fixed UID, GID and GeneratedUID,
# no password, no admin, hidden, and a home on the data volume. An existing
# home is kept as it is (the recreation case). Throwaway.
set -u
t0=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
chown root:wheel $HOMES
chmod 755 $HOMES
echo "$USERS" | while read -r n uid gid ugu ggu; do
  [ -n "$n" ] || continue
  # GeneratedUID first: dscl -create refuses to replace the one that
  # opendirectoryd assigns when the record is made without it.
  dscl . -create /Groups/$n GeneratedUID $ggu
  dscl . -create /Groups/$n PrimaryGroupID $gid
  dscl . -create /Groups/$n RealName "x08 $n"
  dscl . -create /Groups/$n Password '*'
  dscl . -create /Users/$n GeneratedUID $ugu
  dscl . -create /Users/$n UniqueID $uid
  dscl . -create /Users/$n PrimaryGroupID $gid
  dscl . -create /Users/$n NFSHomeDirectory $HOMES/$n
  dscl . -create /Users/$n UserShell /bin/zsh
  dscl . -create /Users/$n RealName "x08 $n"
  dscl . -create /Users/$n Password '*'
  dscl . -create /Users/$n IsHidden 1
  dscl . -append /Groups/$n GroupMembership $n
  if [ -d $HOMES/$n ]; then
    echo "$n: home exists, kept"
  else
    createhomedir -c -u $n 2>&1 | tail -1
    chmod 700 $HOMES/$n
  fi
done
t1=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
echo "TIME users $(echo "$t1 - $t0" | bc) s"
echo "$USERS" | while read -r n uid gid ugu ggu; do
  [ -n "$n" ] || continue
  id $n
  dscl . -read /Users/$n UniqueID PrimaryGroupID GeneratedUID NFSHomeDirectory
  dscl . -read /Groups/$n PrimaryGroupID GeneratedUID
  dsmemberutil getuuid -U $n
  dsmemberutil checkmembership -U $n -G admin
  dsmemberutil checkmembership -U $n -G staff
done
ls -lane $HOMES
