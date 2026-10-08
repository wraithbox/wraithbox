# X04: which clients reread trust without a restart. As root.
#
# The trust variable files start with ca1 only, and the relay signs with ca1.
# Group A starts, one request every 10 s. At 25 s the files get ca1 + ca2
# (as wb-guestd installs the successor on day 30, by replacing each file).
# Group B starts then. At 45 s the relay switches to ca2 (day 58). Group A's
# requests after the switch pass only if the process rereads the file;
# group B's pass if the process read the file after the install.
# Throwaway.
set -u
X=/private/tmp/x04
P=wbp-a
HP=/Users/$P
O=$X/reread
rm -rf $O; mkdir -p $O; chown $P $O
MODE=vars CAS=ca1 sh $X/trust.sh > /dev/null
echo ca1 > $X/state/current
U=https://example.com/
VARS=$(sed 's/^\([A-Z_]*\)=\(.*\)$/\1="\2"/' /etc/wraithbox/env | tr '\n' ' ')
BASEENV="HOME=$HP USER=$P LOGNAME=$P LANG=en_US.UTF-8 TMPDIR=/private/tmp/ PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:$HP/.local/bin"
JH=/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home
C=$X/clients
start() { # group name n command
  eval "sudo -u $P env -i $BASEENV $VARS /bin/sh -c \"cd $O && $4\"" > $O/$1-$2.jsonl 2>&1 &
}
group() {
  n=$2
  start $1 go $n "$X/bin/x04-goclient $U $n 10"
  start $1 node $n "node $C/loop.js $U $n 10"
  start $1 python-urllib $n "python3.14 $C/loop.py urllib $U $n 10"
  start $1 python-requests $n "$HP/venv/bin/python $C/loop.py requests $U $n 10"
  start $1 ruby $n "ruby $C/loop.rb $U $n 10"
  start $1 java $n "$JH/bin/java $C/Loop.java $U $n 10"
}
date -u +"%H:%M:%S group A starts"
group A 8
sleep 25
MODE=vars CAS="ca1 ca2" sh $X/trust.sh | head -1
date -u +"%H:%M:%S files hold ca1 + ca2; group B starts"
group B 5
sleep 20
echo ca2 > $X/state/current
date -u +"%H:%M:%S relay signs with ca2"
wait
date -u +"%H:%M:%S done"
for f in $O/*.jsonl; do
  echo "== $(basename $f .jsonl)"
  cat $f
done
