# X20 option 1, step 2, as root: install Homebrew into /opt/homebrew as the
# toolchain user, through sudo -u (no password from root), with a clean
# environment. First the official installer, then a plain clone.
set -u
U=_wbtool
H=/private/var/wbtool
asT() {
  sudo -u "$U" env -i HOME="$H" USER="$U" LOGNAME="$U" SHELL=/bin/bash \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
    "$@"
}
echo "== official installer, NONINTERACTIVE, as $U"
curl -fsSL -o /tmp/brew-install.sh https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh
chmod 644 /tmp/brew-install.sh
asT /bin/bash -c 'NONINTERACTIVE=1 /bin/bash /tmp/brew-install.sh' > /tmp/brew-install.log 2>&1
echo "installer exit: $?"
tail -15 /tmp/brew-install.log
if [ ! -x /opt/homebrew/bin/brew ]; then
  echo "== plain clone as $U"
  t0=$(date +%s)
  asT /usr/bin/git clone --quiet https://github.com/Homebrew/brew /opt/homebrew 2>&1 | tail -5
  echo "clone seconds: $(( $(date +%s) - t0 ))"
fi
t0=$(date +%s)
asT /opt/homebrew/bin/brew --version
echo "brew --version seconds: $(( $(date +%s) - t0 ))"
asT /opt/homebrew/bin/brew config 2>&1 | head -30
ls -la /opt/homebrew | head -30
