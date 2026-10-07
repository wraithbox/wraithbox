# X20 option 2b, as root: do the relocated bottles still build things? The
# relocation leaves /opt/homebrew in text files such as Python's sysconfig
# data. Expects P=<project user>, HP=<prefix>.
set -u
asP() {
  sudo -u "$P" env -i HOME=/Users/$P USER=$P LOGNAME=$P SHELL=/bin/zsh \
    PATH=$HP/bin:/usr/bin:/bin:/usr/sbin:/sbin LANG=en_US.UTF-8 \
    TMPDIR=/Users/$P/tmp/ HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 "$@"
}
echo "== python sysconfig"
asP $HP/bin/python3.13 -c 'import sysconfig as s; print(s.get_config_var("prefix")); print(s.get_paths()["include"]); print(s.get_config_var("LDFLAGS"))'
echo "== a C extension built from source in a venv"
asP /bin/sh -c "cd /Users/$P/tmp && rm -rf venv && $HP/bin/python3.13 -m venv venv && venv/bin/pip install -q --no-binary :all: markupsafe && venv/bin/python -c 'import markupsafe._speedups as m; print(\"built\", m.__file__)'" 2>&1 | tail -3
echo "== pkg-config for openssl"
asP $HP/bin/pkgconf --cflags --libs openssl
echo "== a C program against the prefix's openssl"
cat > /Users/$P/tmp/t.c <<'EOF'
#include <openssl/opensslv.h>
#include <stdio.h>
int main(void) { printf("%s\n", OPENSSL_VERSION_TEXT); return 0; }
EOF
chown $P /Users/$P/tmp/t.c
asP /bin/sh -c "cd /Users/$P/tmp && cc \$($HP/bin/pkgconf --cflags --libs openssl) t.c -o t && ./t"
echo "== node native addon toolchain (node-gyp config)"
asP $HP/bin/node -p 'process.config.variables.node_prefix'
echo "== /opt/homebrew in files under Cellar, by formula"
grep -rIl /opt/homebrew "$HP/Cellar" 2>/dev/null | cut -d/ -f5 | sort | uniq -c
