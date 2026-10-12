# X08 boot 1: undo b1-18's RealName and UserShell change on wbp1. Throwaway.
dscl . -create /Users/wbp1 RealName "x08 wbp1"
dscl . -create /Users/wbp1 UserShell /bin/zsh
dscl . -read /Users/wbp1 RealName UserShell
