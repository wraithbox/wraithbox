# X04: the clients under test, as root. Homebrew formulas into the shared
# prefix as the toolchain user (X20-shared-homebrew), a project user, and
# Claude Code with its native installer as that user. Before any inspection
# (Apple's NAT network, no proxy). Throwaway.
set -u
U=_wbtool
H=/private/var/wbtool
asT() {
  sudo -u "$U" env -i HOME="$H" USER="$U" LOGNAME="$U" SHELL=/bin/bash \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
    "$@"
}
t0=$(date +%s)
asT /opt/homebrew/bin/brew install git gh go node python@3.14 uv ruby rust openjdk curl 2>&1 | grep -E '^(==>|Error|Warning)' | tail -40
echo "brew seconds: $(( $(date +%s) - t0 ))"
asT /opt/homebrew/bin/brew list --versions

# The project user: a standard user with a home, as wb-guestd creates one.
P=wbp-a
if ! id "$P" >/dev/null 2>&1; then
  sysadminctl -addUser "$P" -fullName "X04 project" -password "$(openssl rand -hex 16)" 2>&1 | tail -2
  createhomedir -c -u "$P" 2>&1 | tail -1
fi
id "$P"
t0=$(date +%s)
sudo -u "$P" -i /bin/bash -c 'curl -fsSL https://claude.ai/install.sh | bash' 2>&1 | tail -8
echo "claude install seconds: $(( $(date +%s) - t0 ))"
ls -la /Users/$P/.local/bin/ 2>&1
