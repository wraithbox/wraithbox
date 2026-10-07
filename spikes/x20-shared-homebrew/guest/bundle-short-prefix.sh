# X20 option 2b, as root: a per-user prefix whose path is no longer than
# /opt/homebrew (13 characters), which Homebrew 7 says it can relocate
# fixed-prefix bottles to. Root creates the prefix and gives it to the project
# user; the project user installs Homebrew and the Brewfile itself.
# Expects P=<project user>, HP=<prefix path or symlink>, B=<Brewfile>, TAG.
# With REAL=<dir>, HP is a symlink to REAL (for a prefix on the data disk).
set -u
if ! id "$P" >/dev/null 2>&1; then
  sysadminctl -addUser "$P" -fullName "x20 $P" -shell /bin/zsh -password "$(openssl rand -hex 16)" -home /Users/"$P" 2>&1 | grep -E 'UID|Error' | grep -v 14120
  createhomedir -c -u "$P" >/dev/null 2>&1
  chmod 700 /Users/"$P"
fi
mkdir -p /Users/$P/tmp && chown $P:staff /Users/$P/tmp
mkdir -p /opt
if [ -n "${REAL:-}" ]; then
  mkdir -p "$REAL" && chown "$P:staff" "$REAL" && chmod 755 "$REAL"
  [ -L "$HP" ] || ln -s "$REAL" "$HP"
else
  mkdir -p "$HP" && chown "$P:staff" "$HP" && chmod 755 "$HP"
fi
ls -ld "$HP" ${REAL:+"$REAL"}
asP() {
  sudo -u "$P" env -i HOME=/Users/$P USER=$P LOGNAME=$P SHELL=/bin/zsh \
    PATH=$HP/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    TMPDIR=/Users/$P/tmp/ HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 \
    HOMEBREW_NO_ENV_HINTS=1 "$@"
}
if [ ! -x $HP/bin/brew ]; then
  t0=$(date +%s)
  asP /usr/bin/git clone --quiet https://github.com/Homebrew/brew "$HP/" 2>&1 | tail -3
  echo "brew clone seconds: $(( $(date +%s) - t0 ))"
fi
asP $HP/bin/brew --prefix
cp "$B" /Users/$P/Brewfile.$TAG && chown $P:staff /Users/$P/Brewfile.$TAG
L=/private/tmp/x20-$TAG.log
t0=$(date +%s)
asP $HP/bin/brew bundle install --no-upgrade --verbose --file=/Users/$P/Brewfile.$TAG > $L 2>&1
rc=$?
echo "bundle exit: $rc seconds: $(( $(date +%s) - t0 ))"
grep -E '^(Warning|Error)' $L | sort | uniq -c | head -20
echo "poured: $(grep -c '^==> Pouring' $L)"
asP $HP/bin/brew info --json=v2 --installed > /private/tmp/x20-$TAG-info.json 2>/dev/null
/usr/bin/python3 - /private/tmp/x20-$TAG-info.json <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
src = [f["name"] for f in d["formulae"] if not f["installed"][0]["poured_from_bottle"]]
print("installed kegs:", len(d["formulae"]), "built from source:", len(src), src)
EOF
echo "prefix: $(du -sh "$HP/" | cut -f1)"
echo "== the relocated tools run, as $P"
for t in git node python3.13 psql; do
  printf '%-10s %s\n' "$t" "$(asP /bin/sh -c "command -v $t >/dev/null && $t --version 2>&1 | head -1")"
done
asP /bin/sh -c "PATH=$HP/opt/postgresql@17/bin:\$PATH; psql --version"
asP $HP/bin/python3.13 -c 'import ssl, sqlite3, sys; print(sys.prefix, ssl.OPENSSL_VERSION, sqlite3.sqlite_version)'
asP $HP/bin/git -C /Users/$P clone --quiet --depth 1 https://github.com/Homebrew/brew /Users/$P/tmp/clonetest 2>&1 | tail -1; echo "git https clone exit $?"
echo "== hard-coded /opt/homebrew left in the prefix"
grep -rIl /opt/homebrew "$HP/Cellar" 2>/dev/null | wc -l
grep -rIl /opt/homebrew "$HP/Cellar" 2>/dev/null | head -5
echo "== the other project user can't write here"
sudo -u wbp-a /usr/bin/touch "$HP/bin/x20-planted" 2>&1
