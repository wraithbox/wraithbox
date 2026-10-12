# X08 boot 1: plant the host-built euid probe setuid root in wbp2's home
# (replacing the killed copy of id) and run it as wbp2 with nosuid. Throwaway.
set -u
H=/Volumes/wbhomes
rm -f $H/wbp2/suid-id $H/wbp2/plain-id
cp /var/root/euid $H/wbp2/suid-euid
chown root:wheel $H/wbp2/suid-euid
chmod 4755 $H/wbp2/suid-euid
ls -l $H/wbp2/suid-euid
echo "control on the system Data volume (/var/root is 700, so via /private/tmp):"
cp /var/root/euid /private/tmp/x08-euid; chown root:wheel /private/tmp/x08-euid; chmod 4755 /private/tmp/x08-euid
sudo -u wbp2 /private/tmp/x08-euid; rm -f /private/tmp/x08-euid
echo "nosuid mount:"; mount | grep " $H "
sudo -u wbp2 $H/wbp2/suid-euid
