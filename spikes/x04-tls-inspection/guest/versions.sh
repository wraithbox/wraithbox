# X04: the guest's network interface and the versions of the clients. Throwaway.
route -n get default | grep interface
ifconfig en0 | grep inet
grep -v '^#' /etc/pf.conf
B=/opt/homebrew/bin
sw_vers | tr '\n' ' '; echo
xcodebuild -version | tr '\n' ' '; echo
/usr/bin/curl --version | head -1
$B/../opt/curl/bin/curl --version | head -1
/usr/bin/git --version
$B/git --version
$B/gh --version | head -1
$B/go version
$B/node --version
$B/npm --version
$B/python3.14 --version
$B/python3.14 -m pip --version
$B/uv --version
$B/ruby --version
$B/gem --version
$B/cargo --version
/opt/homebrew/opt/openjdk/bin/java -version 2>&1 | head -1
/Users/wbp-a/.local/bin/claude --version
BUN_BE_BUN=1 /Users/wbp-a/.local/bin/claude --version
swift --version 2>&1 | head -1
ls /usr/bin/ruby /usr/bin/python3 2>&1
