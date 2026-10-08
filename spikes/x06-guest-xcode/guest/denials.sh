# X06-guest-xcode: run as root. The Seatbelt denials of the last N minutes
# (X06_MIN, default 2), one line per process, operation and path, counted.
# Throwaway.
m=${X06_MIN:-2}
log show --last "${m}m" --style compact --predicate 'sender == "Sandbox" OR eventMessage CONTAINS "Sandbox:"' 2>/dev/null |
  grep -o 'Sandbox: [^(]*([0-9]*) deny([0-9]*) [^ ]* .*' |
  sed -E 's/\([0-9]+\) deny\([0-9]+\)//' |
  sort | uniq -c | sort -rn | head -${X06_TOP:-200}
