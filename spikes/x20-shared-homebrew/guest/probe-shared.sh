# X20 option 1, as root: what can a project user change in the shared prefix?
# Each line prints ALLOWED or DENIED. Any ALLOWED write into /opt/homebrew is
# a finding against SEC08-proj-isolation.
set -u
P=wbp-a
asP() {
  sudo -u "$P" env -i HOME=/Users/$P USER=$P LOGNAME=$P SHELL=/bin/zsh \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    TMPDIR=/Users/$P/tmp/ HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$@"
}
mkdir -p /Users/$P/tmp && chown $P:staff /Users/$P/tmp
try() { # try <label> <command...>
  label=$1; shift
  if out=$(asP "$@" 2>&1); then r=ALLOWED; else r=DENIED; fi
  printf '%-7s %-44s %s\n' "$r" "$label" "$(printf '%s' "$out" | tail -1 | cut -c1-110)"
}
echo "== writes into the prefix, as $P"
try "replace bin/git symlink"        /bin/ln -sf /usr/bin/true /opt/homebrew/bin/git
try "write into bin/"                /usr/bin/touch /opt/homebrew/bin/x20-planted
try "overwrite git binary in Cellar" /bin/sh -c 'echo x >> /opt/homebrew/Cellar/git/*/bin/git'
try "write into Cellar/"             /bin/mkdir /opt/homebrew/Cellar/x20-planted
try "edit brew's Ruby"               /bin/sh -c 'echo "# x" >> /opt/homebrew/Library/Homebrew/brew.rb'
try "write etc/ (openssl, redis conf)" /usr/bin/touch /opt/homebrew/etc/x20-planted
try "write var/ (database dirs)"     /usr/bin/touch /opt/homebrew/var/x20-planted
try "write lib/node_modules"         /bin/mkdir /opt/homebrew/lib/node_modules/x20-planted
try "write python site-packages"     /bin/sh -c 'touch /opt/homebrew/lib/python3.13/site-packages/x20-planted'
try "chmod a prefix dir"             /bin/chmod 777 /opt/homebrew/bin
echo "== package managers, as $P"
try "brew install jq"                /opt/homebrew/bin/brew install jq
try "brew uninstall jq"              /opt/homebrew/bin/brew uninstall --ignore-dependencies jq
try "brew link --overwrite node@22"  /opt/homebrew/bin/brew link --overwrite node@22
try "brew update"                    /opt/homebrew/bin/brew update
try "npm install -g (default prefix)" /opt/homebrew/bin/npm install -g --no-audit --no-fund left-pad
try "pip3 install (system site)"     /opt/homebrew/bin/pip3.13 install --break-system-packages six
try "pip3 install --user"            /opt/homebrew/bin/pip3.13 install --user --break-system-packages six
try "npm -g with NPM_CONFIG_PREFIX=~" /usr/bin/env NPM_CONFIG_PREFIX=/Users/$P/.npm-global /opt/homebrew/bin/npm install -g --no-audit --no-fund left-pad
try "uv tool install (to ~)"         /opt/homebrew/bin/uv tool install ruff
try "go install (to ~/go)"           /opt/homebrew/bin/go install golang.org/x/tools/cmd/stringer@latest
try "initdb in home"                 /opt/homebrew/opt/postgresql@17/bin/initdb -D /Users/$P/pgdata
echo "== reach the toolchain user, as $P"
try "sudo -n -u _wbtool true"        /usr/bin/sudo -n -u _wbtool /usr/bin/true
try "read toolchain user's home"     /bin/ls /private/var/wbtool
try "su _wbtool"                     /usr/bin/su _wbtool -c true
echo "== what the prefix looks like, as root"
echo "not owned by _wbtool:   $(find /opt/homebrew ! -user _wbtool | wc -l)"
echo "group not _wbtool:      $(find /opt/homebrew ! -group _wbtool | wc -l)"
echo "group-writable, group not _wbtool: $(find /opt/homebrew -perm -g+w ! -group _wbtool ! -type l | wc -l)"
echo "world-writable (not symlinks): $(find /opt/homebrew -perm -o+w ! -type l | wc -l)"
find /opt/homebrew -perm -o+w ! -type l | head -5
echo "setuid or setgid: $(find /opt/homebrew \( -perm -4000 -o -perm -2000 \) | wc -l)"
echo "== which tools each project gets on the default PATH, as $P"
for t in git node python3 python3.12 go psql mariadb; do
  printf '%-10s %s\n' "$t" "$(asP /bin/sh -c "command -v $t && $t --version 2>&1 | head -1" | tr '\n' ' ')"
done
echo "== project B's versions through opt/ paths, as $P"
asP /bin/sh -c 'PATH=/opt/homebrew/opt/node@22/bin:/opt/homebrew/opt/python@3.12/libexec/bin:/opt/homebrew/opt/postgresql@16/bin:/opt/homebrew/opt/go@1.25/bin:$PATH; node --version; python3 --version; python --version; psql --version; go version'
