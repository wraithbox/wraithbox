# X08 boot 1: give wbp1 and wbp2 the GeneratedUID from host state, which
# mkusers.sh's first version failed to set (b1-04: -change works). Throwaway.
set -u
fix() {
  old=$(dscl . -read /Users/$1 GeneratedUID | awk '{print $2}')
  dscl . -change /Users/$1 GeneratedUID "$old" "$2"
  dscacheutil -flushcache
  echo "$1: $old -> $(dscl . -read /Users/$1 GeneratedUID | awk '{print $2}') dsmemberutil $(dsmemberutil getuuid -U $1)"
}
fix wbp1 CBBC08A1-0DB9-4290-B899-F55CD77080A2
fix wbp2 63095303-1145-4845-8BFD-B9B31627347C
