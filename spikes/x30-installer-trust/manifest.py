"""Rewrite the KernelCache and RestoreKernelCache digests of a BuildManifest.

The digest of KernelCache is the SHA-384 of the IM4P file. The digest of
RestoreKernelCache is the SHA-384 of the same file with its IM4P type tag
(bytes 14 to 18, "krnl") changed to "rkrn". Both were checked against the
27.2 (26B123) manifest. Every build identity whose component has the path
<entry> gets the digests of <new file>. Run with python3 -I.

Usage: manifest.py <BuildManifest.plist> <entry> <new file> <out plist>
"""

import hashlib
import json
import plistlib
import sys

src, entry, newfile, out = sys.argv[1:5]
m = plistlib.load(open(src, "rb"))
data = bytearray(open(newfile, "rb").read())
krnl = hashlib.sha384(data).digest()
data[14:18] = b"rkrn"
rkrn = hashlib.sha384(data).digest()
changed = 0
for ident in m["BuildIdentities"]:
    for comp, digest in (("KernelCache", krnl), ("RestoreKernelCache", rkrn)):
        c = ident["Manifest"].get(comp)
        if c and c.get("Info", {}).get("Path") == entry:
            c["Digest"] = digest
            changed += 1
with open(out, "xb") as f:
    plistlib.dump(m, f, fmt=plistlib.FMT_XML)
print(json.dumps({"entry": entry, "components_changed": changed,
                  "krnl": krnl.hex(), "rkrn": rkrn.hex()}))
