# X08: a comparable snapshot of the homes on the data volume: numeric owner,
# group, mode, flags, size, ACL (as ls prints it), xattrs, and a checksum of
# every regular file. Times are left out: they differ by design. Throwaway.
set -u
H=${H:-/Volumes/wbhomes}
cd $H || exit 1
find ${U1:-wbp1} ${U2:-wbp2} -print | LC_ALL=C sort | while IFS= read -r p; do
  st=$(stat -f '%u %g %Sp %Sf %z' "$p")
  case $(stat -f '%HT' "$p") in
    "Regular File") sum=$(shasum -a 256 "$p" | cut -c1-16) ;;
    "Symbolic Link") sum="-> $(readlink "$p")" ;;
    *) sum=- ;;
  esac
  [ -d "$p" ] && st=$(stat -f '%u %g %Sp %Sf' "$p")
  echo "$p $st $sum"
  ls -lde "$p" | sed -n '2,$p' | sed 's/^/    acl/'
  xattr "$p" 2>/dev/null | sed 's/^/    xattr /'
done
