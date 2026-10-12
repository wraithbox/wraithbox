# X08: run the planted setuid probe as wbs2 on the HFS+ volume. Throwaway.
sudo -u wbs2 /private/var/wbhfs/wbs2/suid-euid
mount | grep wbhfs
