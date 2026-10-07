# X20 option 1, as root: reconcile one project's Brewfile into the shared
# prefix as the toolchain user. Expects B=<guest path of the Brewfile> and
# TAG=<label for the log>. Prints the time and the tail of the log.
set -u
U=_wbtool
H=/private/var/wbtool
asT() {
  sudo -u "$U" env -i HOME="$H" USER="$U" LOGNAME="$U" SHELL=/bin/bash \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
    "$@"
}
chmod 644 "$B"
t0=$(date +%s)
asT /opt/homebrew/bin/brew bundle install --no-upgrade --verbose --file="$B" > "/tmp/x20-$TAG.log" 2>&1
rc=$?
echo "bundle exit: $rc seconds: $(( $(date +%s) - t0 ))"
grep -E '^(==> (Pouring|Installing|Fetching)|Error|Warning|.*conflict)' "/tmp/x20-$TAG.log" | head -80
tail -15 "/tmp/x20-$TAG.log"
echo "cellar: $(ls /opt/homebrew/Cellar | wc -l) kegs, $(du -sh /opt/homebrew/Cellar | cut -f1)"
echo "cache: $(du -sh $H/Library/Caches/Homebrew 2>/dev/null | cut -f1)"
