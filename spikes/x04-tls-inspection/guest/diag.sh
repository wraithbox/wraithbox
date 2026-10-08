# X04: relay and pf diagnostics. Throwaway.
X=/private/tmp/x04
ps axo pid,user,command | grep '[x]04-proxy'
cat $X/proxy.err
ls -la $X $X/state
pfctl -s info 2>/dev/null | head -3
pfctl -s state 2>/dev/null | grep -E '443|8443' | head
tail -5 $X/proxy.jsonl
