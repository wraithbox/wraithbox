"""Write a changed copy of a restore image (a zip archive).

Every entry is copied in order, stored (no compression), with a correct
CRC, so the zip itself stays valid and only the content changes. Run with
python3 -I.

Usage:
  mkipsw.py <src.ipsw> <dst.ipsw> [--flip ENTRY[:OFFSET]]... [--put ENTRY=FILE]...

--flip  XOR one byte of ENTRY with 0x01, at OFFSET (default: the middle).
--put   replace ENTRY's content with FILE's.

Prints one JSON line per changed entry, with the SHA-384 before and after.
"""

import hashlib
import json
import sys
import zipfile

CHUNK = 8 << 20


def parse(args):
    src, dst, rest = args[0], args[1], args[2:]
    flips, puts = {}, {}
    while rest:
        flag, val = rest[0], rest[1]
        rest = rest[2:]
        if flag == "--flip":
            name, _, off = val.partition(":")
            flips[name] = int(off) if off else None
        elif flag == "--put":
            name, _, path = val.partition("=")
            puts[name] = path
        else:
            sys.exit(f"unknown flag {flag}")
    return src, dst, flips, puts


def copy(zin, zout, info, flips, puts):
    out = zipfile.ZipInfo(info.filename, date_time=info.date_time)
    out.external_attr = info.external_attr
    out.compress_type = zipfile.ZIP_STORED
    if info.is_dir():
        zout.writestr(out, b"")
        return None
    before, after = hashlib.sha384(), hashlib.sha384()
    name = info.filename
    flip_at = None
    if name in flips:
        flip_at = flips[name] if flips[name] is not None else info.file_size // 2
    src = open(puts[name], "rb") if name in puts else zin.open(info)
    orig = zin.open(info) if name in puts else None
    pos = 0
    with zout.open(out, "w", force_zip64=True) as w:
        while True:
            buf = src.read(CHUNK)
            if not buf:
                break
            if orig is None:
                before.update(buf)
            if flip_at is not None and pos <= flip_at < pos + len(buf):
                b = bytearray(buf)
                b[flip_at - pos] ^= 0x01
                buf = bytes(b)
            after.update(buf)
            w.write(buf)
            pos += len(buf)
    if orig is not None:
        while True:
            buf = orig.read(CHUNK)
            if not buf:
                break
            before.update(buf)
    src.close()
    if name in flips or name in puts:
        return {
            "entry": name,
            "flip_at": flip_at,
            "put": puts.get(name),
            "size": pos,
            "sha384_before": before.hexdigest(),
            "sha384_after": after.hexdigest(),
        }
    return None


def main():
    src, dst, flips, puts = parse(sys.argv[1:])
    seen = set()
    with zipfile.ZipFile(src) as zin, zipfile.ZipFile(dst, "x", allowZip64=True) as zout:
        for info in zin.infolist():
            r = copy(zin, zout, info, flips, puts)
            seen.add(info.filename)
            if r:
                print(json.dumps(r), flush=True)
    missing = (set(flips) | set(puts)) - seen
    if missing:
        sys.exit(f"entries not found: {sorted(missing)}")


main()
