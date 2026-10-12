# X08 boot 3: is a job launchd starts on its own (not attributed to
# x17-guestd) also refused on the USB volume? Throwaway.
set -u
rm -f /private/tmp/x08-ls.out
launchctl submit -l org.wraithbox.x08-ls -o /private/tmp/x08-ls.out -e /private/tmp/x08-ls.out -- /bin/ls -la /private/var/wbhomes
sleep 3
launchctl remove org.wraithbox.x08-ls
cat /private/tmp/x08-ls.out
true
