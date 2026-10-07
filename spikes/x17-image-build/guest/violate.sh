# X17-image-build: turn Remote Login on, as a provisioning step that uses SSH
# would leave it, so the scan can be seen to fail on it. Run as root by
# x17-guestd. Throwaway.
launchctl enable system/com.openssh.sshd
launchctl bootstrap system /System/Library/LaunchDaemons/ssh.plist 2>&1
sleep 1
launchctl print-disabled system | grep ssh
netstat -anp tcp | grep LISTEN
