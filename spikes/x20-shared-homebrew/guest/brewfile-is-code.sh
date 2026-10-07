# X20 option 1, as root: a Brewfile is Ruby, and brew bundle runs it as the
# toolchain user. So does a formula file passed by path. Either one lets the
# project that wrote it write into the shared prefix.
set -u
U=_wbtool
H=/private/var/wbtool
asT() {
  sudo -u "$U" env -i HOME="$H" USER="$U" LOGNAME="$U" SHELL=/bin/bash \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
    "$@"
}
cat > /tmp/Brewfile.evil <<'EOF'
system "/usr/bin/touch", "/opt/homebrew/bin/x20-from-brewfile"
brew "jq"
EOF
chmod 644 /tmp/Brewfile.evil
echo "== brew bundle with a Brewfile that runs a command"
asT /opt/homebrew/bin/brew bundle install --no-upgrade --file=/tmp/Brewfile.evil 2>&1 | tail -2
ls -l /opt/homebrew/bin/x20-from-brewfile 2>&1
rm -f /opt/homebrew/bin/x20-from-brewfile
cat > /tmp/x20evil.rb <<'EOF'
class X20evil < Formula
  desc "x20"
  homepage "https://example.invalid"
  url "https://example.invalid/x.tar.gz"
  version "1"
  File.write("/opt/homebrew/bin/x20-from-formula-file", "x")
end
EOF
chmod 644 /tmp/x20evil.rb
echo "== brew install of a formula file by path"
asT /opt/homebrew/bin/brew install /tmp/x20evil.rb 2>&1 | tail -3
ls -l /opt/homebrew/bin/x20-from-formula-file 2>&1
rm -f /opt/homebrew/bin/x20-from-formula-file
echo "== brew install --formula of names only (what wb-guestd would run)"
t0=$(date +%s)
asT /opt/homebrew/bin/brew install --formula jq tree 2>&1 | tail -2
echo "seconds: $(( $(date +%s) - t0 ))"
