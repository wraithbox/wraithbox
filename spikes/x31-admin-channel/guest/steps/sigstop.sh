p=$(pgrep -f libexec/wb-guestd); kill -$1 $p; echo "kill -$1 $p rc=$?"; ps -o pid,stat,command -p $p
