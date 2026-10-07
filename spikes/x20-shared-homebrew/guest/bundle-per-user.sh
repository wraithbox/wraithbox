# X20 option 2, as root: a Homebrew prefix in the project user's own home
# (~/homebrew), installed and used by the project user itself, which needs no
# sudo. Expects P=<project user>, B=<guest path of the Brewfile>, TAG=<label>.
set -u
HP=/Users/$P/homebrew
asP() {
  sudo -u "$P" env -i HOME=/Users/$P USER=$P LOGNAME=$P SHELL=/bin/zsh \
    PATH=$HP/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    TMPDIR=/Users/$P/tmp/ HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 \
    HOMEBREW_NO_ENV_HINTS=1 "$@"
}
mkdir -p /Users/$P/tmp && chown $P:staff /Users/$P/tmp
if [ ! -x $HP/bin/brew ]; then
  t0=$(date +%s)
  asP /usr/bin/git clone --quiet https://github.com/Homebrew/brew $HP 2>&1 | tail -3
  asP $HP/bin/brew --version
  echo "brew clone seconds: $(( $(date +%s) - t0 ))"
fi
cp "$B" /Users/$P/Brewfile.$TAG && chown $P:staff /Users/$P/Brewfile.$TAG
L=/private/tmp/x20-$TAG.log
t0=$(date +%s)
asP $HP/bin/brew bundle install --no-upgrade --verbose --file=/Users/$P/Brewfile.$TAG > $L 2>&1
rc=$?
echo "bundle exit: $rc seconds: $(( $(date +%s) - t0 ))"
grep -E '^(Warning|Error)' $L | sort | uniq -c | head -20
echo "poured: $(grep -c '^==> Pouring' $L)"
asP $HP/bin/brew info --json=v2 --installed > /private/tmp/x20-$TAG-info.json 2>/dev/null
/usr/bin/python3 - /private/tmp/x20-$TAG-info.json <<'EOF' 2>/dev/null || plutil -p /private/tmp/x20-$TAG-info.json | head -5
import json, sys
d = json.load(open(sys.argv[1]))
src = [f["name"] for f in d["formulae"] if not f["installed"][0]["poured_from_bottle"]]
print("installed kegs:", len(d["formulae"]), "built from source:", len(src), src)
EOF
echo "prefix: $(du -sh $HP | cut -f1), cellar $(du -sh $HP/Cellar | cut -f1)"
echo "cache: $(du -sh /Users/$P/Library/Caches/Homebrew 2>/dev/null | cut -f1)"
tail -5 $L
