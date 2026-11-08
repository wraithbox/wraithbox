# X30 results (2026-11-08, dedicated Mac, macOS 27.2 26B123)

Restore image: UniversalMac_27.2_26B123_Restore.ipsw. Each case: one
confined install (profiles/vmd-install.sb), 8 GiB, run by run-case.sh.
`<case>.summary.txt` has the log lines the result rests on (summarize.sh);
the full logs (`*.log.ndjson`, 1 MB to 6 MB) and nettop samples stay out of git.

| Case | Change | Exit | After | Error |
|---|---|---|---|---|
| c00-original | none | 0 | 446 s installed | |
| c01-rezip | every entry re-stored, no content change | 0 | 143 s installed | |
| c02-kernel-flip | 1 byte of kernelcache.release.vma2 | 1 | 9.4 s, 0 % | MobileRestore 3194, TSS error 94 |
| c06-kernel-flip-manifest | c02 + BuildManifest kernel digests rewritten | 1 | 9.7 s, 0 % | MobileRestore 3194, TSS error 94 |
| c04-kernel-swap | kernel cache replaced by kernelcache.release.mac17g (Apple-signed, other device) | 1 | 12.5 s, 0 % | MobileRestore 3194, TSS error 94 |
| c03-os-flip | 1 byte in the middle of 141-12228-170.dmg.aea (system volume, 11 GB) | 1 | 219 s, 48 % | MobileRestore -1 / AMRestore 1106 "failed to restore APFS image" |

c05-kernel-swap-manifest was not run (c04 and c06 cover it).
