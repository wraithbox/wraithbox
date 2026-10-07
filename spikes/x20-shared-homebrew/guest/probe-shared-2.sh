# X20 option 1, follow-up to probe-shared.sh: the two ALLOWED package manager
# lines, and the Cellar write with the path spelled out.
set -u
P=wbp-a
asP() {
  sudo -u "$P" env -i HOME=/Users/$P USER=$P LOGNAME=$P SHELL=/bin/zsh \
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    TMPDIR=/Users/$P/tmp/ HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$@"
}
g=$(ls -d /opt/homebrew/Cellar/git/*/bin/git)
echo "== append to $g"
asP /bin/sh -c "echo x >> $g"; echo "exit $?"
echo "== brew install tree (not installed yet)"
asP /opt/homebrew/bin/brew install tree 2>&1 | tail -4; echo "exit ${PIPESTATUS[0]:-?}"
ls -d /opt/homebrew/Cellar/tree 2>&1
echo "== earlier jq answer"
asP /opt/homebrew/bin/brew install jq 2>&1 | tail -3
echo "== where pip put six"
ls -d /opt/homebrew/lib/python3.13/site-packages/six* 2>&1
ls -d /Users/$P/Library/Python/3.13/lib/python/site-packages/six* 2>&1
asP /opt/homebrew/bin/pip3.13 install --break-system-packages --force-reinstall six 2>&1 | head -2
echo "== PEP 668 without the override"
asP /opt/homebrew/bin/pip3.13 install six 2>&1 | head -3
