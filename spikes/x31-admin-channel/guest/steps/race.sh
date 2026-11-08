# As root: approach step 5 without a boot. Two processes run the product's
# update at start at the same instant into one install directory, the
# wb-guestd split and the wb-guestroot split, 200 rounds with the directory
# missing (both make it) and 200 rounds with it there (as at a boot).
set -e
T=/tmp/x31
rm -rf $T/src /private/var/x31-race
mkdir -p $T/src
cp $T/wb-guestd.2 $T/src/wb-guestd
cp $T/x31-guestroot.2 $T/src/wb-guestroot
printf '%s' 0.0.0-x31.2 > $T/src/VERSION
chown -R root:wheel $T/src
install -d -m 755 -o root -g wheel /private/var/x31-race
/usr/local/bin/x31-race parent 200 /private/var/x31-race/fresh $T/src fresh
/usr/local/bin/x31-race parent 200 /private/var/x31-race/existing $T/src existing
rm -rf /private/var/x31-race
