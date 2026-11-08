# Kill wb-guestd with SIGKILL whenever it runs, for $1 seconds, in the background.
nohup sh -c "end=\$((\$(date +%s)+$1)); n=0; while [ \$(date +%s) -lt \$end ]; do if pkill -9 -f libexec/wb-guestd; then n=\$((n+1)); echo \"\$(date +%T) kill \$n\"; fi; sleep 0.2; done" >/var/root/x31-crashloop.log 2>&1 &
echo started
