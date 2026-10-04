#!/bin/bash
# Usage: denials.sh <seconds> <process-name>
# Prints the sandbox denials the kernel logged for a process name.
/usr/bin/log show --last "${1}s" --style compact --predicate 'sender == "Sandbox"' 2>/dev/null |
  grep -F "$2(" | grep 'deny(' | sed -E 's/^.*Sandbox: //' | sort | uniq -c
