# X20 option 1, step 1, as root: the "create the toolchain user" and "create a
# tool prefix owned by the toolchain user" named operations of #144 and #150.
# The name _wbtool is a stand-in: the maintainer hasn't picked one (#144).
set -eu
U=_wbtool
H=/private/var/wbtool
# A free id below 500, as macOS service accounts have.
id=440
while dscl . -list /Users UniqueID | awk '{print $2}' | grep -qx "$id" ||
      dscl . -list /Groups PrimaryGroupID | awk '{print $2}' | grep -qx "$id"; do id=$((id + 1)); done
dscl . -create /Groups/$U
dscl . -create /Groups/$U PrimaryGroupID "$id"
dscl . -create /Groups/$U RealName "Wraith Box toolchain"
dscl . -create /Groups/$U Password '*'
dscl . -create /Users/$U
dscl . -create /Users/$U UniqueID "$id"
dscl . -create /Users/$U PrimaryGroupID "$id"
dscl . -create /Users/$U RealName "Wraith Box toolchain"
dscl . -create /Users/$U NFSHomeDirectory "$H"
dscl . -create /Users/$U UserShell /usr/bin/false
dscl . -create /Users/$U Password '*'
dscl . -create /Users/$U IsHidden 1
mkdir -p "$H"
chown "$U:$U" "$H"
chmod 700 "$H"
# The shared prefix: owned by the toolchain user, read and execute for all.
mkdir -p /opt/homebrew
chown "$U:$U" /opt/homebrew
chmod 755 /opt/homebrew
id "$U"
dscacheutil -q user -a name "$U"
ls -ld /opt/homebrew "$H"
dsmemberutil checkmembership -U "$U" -G admin
dsmemberutil checkmembership -U "$U" -G staff
