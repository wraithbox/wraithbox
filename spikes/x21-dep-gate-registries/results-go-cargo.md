| project | eco | install | off: rc, downloads, too young | refuse: rc, refused | filter: rc, versions hidden, backstop refusals, versions not in off run |
|---|---|---|---|---|---|
| go | go | lock | 0, 179, 0 | 0, 0 | 0, 0, 0, 0 | ? / ? / ? |
| go | go | fresh | 0, 21, 1 | 1, 1 | 0, 76, 0, 1 | ? / ? / ? |
| cargo | cargo | lock | 0, 316, 0 | 0, 0 | 0, 28, 0, 0 | ? / ? / ? |
| cargo | cargo | fresh | 0, 283, 12 | 1, 1 | 0, 16, 0, 13 | ? / ? / ? |

fresh filter: 0/2 failed
fresh off   : 0/2 failed
fresh refuse: 2/2 failed
lock  filter: 0/2 failed
lock  off   : 0/2 failed
lock  refuse: 0/2 failed

Go exit codes: the go command was run by othereco.py and its exit codes are
in its output (lock 0/0/0, fresh 0/1/0 for off/refuse/filter); the "?"
times were not recorded. The Go age check here uses the `.info` Time,
which is the commit time (see mapping.py go), not the publish time.
