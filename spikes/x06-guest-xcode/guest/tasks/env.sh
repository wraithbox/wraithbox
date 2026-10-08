# X06 task: what a project user started by x06-guestd sees. Throwaway.
id
echo "tty: $(tty)"
echo "managername: $(launchctl managername 2>&1)"
echo "TMPDIR=${TMPDIR-unset}"
echo "DARWIN_USER_TEMP_DIR=$(getconf DARWIN_USER_TEMP_DIR)"
echo "DARWIN_USER_CACHE_DIR=$(getconf DARWIN_USER_CACHE_DIR)"
launchctl print user/$(id -u) 2>&1 | sed -n '1,12p'
launchctl print gui/$(id -u) 2>&1 | sed -n '1,3p'
security list-keychains 2>&1
security default-keychain 2>&1
ps -o pid,ppid,pgid,sess,tty,user,command -p $$
