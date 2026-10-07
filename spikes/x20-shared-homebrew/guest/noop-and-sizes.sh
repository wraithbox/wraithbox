# X20, as root: how long a reconcile takes when everything is installed (the
# common session start), and how much disk each layout uses.
set -u
U=_wbtool
H=/private/var/wbtool
asT() {
  sudo -u "$U" env -i HOME="$H" USER="$U" LOGNAME="$U" SHELL=/bin/bash \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
    HOMEBREW_NO_INSTALL_UPGRADE=1 "$@"
}
names=$(sed -n 's/^brew "\(.*\)"$/\1/p' /private/tmp/Brewfile.a | tr '\n' ' ')
for i in 1 2 3; do
  t0=$(perl -MTime::HiRes=time -e 'printf "%.2f", time')
  asT /opt/homebrew/bin/brew install --formula $names > /dev/null 2>&1
  rc=$?
  t1=$(perl -MTime::HiRes=time -e 'printf "%.2f", time')
  echo "no-op brew install --formula (A), run $i: exit $rc, $(echo "$t1 - $t0" | bc) s"
done
for i in 1 2 3; do
  t0=$(perl -MTime::HiRes=time -e 'printf "%.2f", time')
  asT /opt/homebrew/bin/brew bundle check --file=/private/tmp/Brewfile.a > /dev/null 2>&1
  rc=$?
  t1=$(perl -MTime::HiRes=time -e 'printf "%.2f", time')
  echo "brew bundle check (A), run $i: exit $rc, $(echo "$t1 - $t0" | bc) s"
done
echo "== disk"
echo "shared /opt/homebrew (A+B+tree): $(du -sh /opt/homebrew | cut -f1), Cellar $(du -sh /opt/homebrew/Cellar | cut -f1), cache $(du -sh $H/Library/Caches/Homebrew | cut -f1)"
echo "per-user /opt/wbp-c (A): $(du -sh /opt/wbp-c/ | cut -f1), Cellar $(du -sh /opt/wbp-c/Cellar | cut -f1), cache $(du -sh /Users/wbp-c/Library/Caches/Homebrew | cut -f1)"
echo "Homebrew itself (Library): $(du -sh /opt/wbp-c/Library | cut -f1)"
