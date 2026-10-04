#!/usr/bin/env python3
"""Throwaway: publish-time sources for the Go module proxy, crates.io and Homebrew.

go:       how far the .info Time (commit time) is from the time the module
          proxy first saw the version (index.golang.org), and how big a
          7-day window of the index is.
crates:   whether every sparse index line carries pubtime.
homebrew: how many of the most-installed formulae have a bottle younger than
          7 days (org.opencontainers.image.created on the ghcr manifest).
"""

import concurrent.futures as cf
import datetime as dt
import json
import sys
import urllib.parse
import urllib.request

NOW = dt.datetime.now(dt.timezone.utc)
WEEK = dt.timedelta(days=7)


def get(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def ts(s):
    t = dt.datetime.fromisoformat(s.replace("Z", "+00:00"))
    return t if t.tzinfo else t.replace(tzinfo=dt.timezone.utc)


def go_escape(p):
    return "".join("!" + c.lower() if c.isupper() else c for c in p)


def go():
    # 1. size of a 7-day window of the index
    since = (NOW - WEEK).strftime("%Y-%m-%dT%H:%M:%SZ")
    total = 0
    sample = []
    cursor = since
    pages = 0
    while True:
        body = get(f"https://index.golang.org/index?since={cursor}&limit=2000").decode()
        lines = [json.loads(l) for l in body.splitlines() if l.strip()]
        if not lines:
            break
        total += len(lines)
        pages += 1
        if pages % 10 == 1:
            sample.extend(lines[::50])
        if len(lines) < 2000:
            break
        cursor = lines[-1]["Timestamp"]
    print(f"go: index entries in the last 7 days: {total} ({pages} pages of 2000)")
    # 2. .info Time vs first-seen time for tagged (non-pseudo) versions
    tagged = [e for e in sample if "-0." not in e["Version"] and "-" not in e["Version"].split("+")[0][1:]][:200]

    def info(e):
        try:
            d = json.loads(get(f"https://proxy.golang.org/{go_escape(e['Path'])}/@v/{go_escape(e['Version'])}.info"))
            return e, ts(d["Time"])
        except Exception:
            return e, None

    older = 0
    n = 0
    gaps = []
    with cf.ThreadPoolExecutor(16) as ex:
        for e, t in ex.map(info, tagged):
            if t is None:
                continue
            n += 1
            seen = ts(e["Timestamp"])
            gap = (seen - t).total_seconds() / 86400
            gaps.append(gap)
            if seen - t > WEEK:
                older += 1
    gaps.sort()
    print(f"go: tagged versions first seen in the last 7 days, sampled: {n}; .info Time more than 7 days before first-seen: {older} ({100 * older / max(n, 1):.0f}%)")
    if gaps:
        print(f"go: gap first-seen minus .info Time, days: median {gaps[len(gaps) // 2]:.1f}, p90 {gaps[int(len(gaps) * 0.9)]:.1f}, max {gaps[-1]:.0f}")


def crates():
    names = ["serde", "tokio", "rand", "libc", "syn", "clap", "regex", "anyhow", "hyper", "reqwest",
             "itoa", "log", "bitflags", "cfg-if", "memchr", "once_cell", "proc-macro2", "quote", "unicode-ident", "a"]

    def path(n):
        n = n.lower()
        if len(n) <= 2:
            return f"{len(n)}/{n}"
        if len(n) == 3:
            return f"3/{n[0]}/{n}"
        return f"{n[:2]}/{n[2:4]}/{n}"

    lines = missing = young = 0
    for n in names:
        for l in get("https://index.crates.io/" + path(n)).decode().splitlines():
            d = json.loads(l)
            lines += 1
            if "pubtime" not in d:
                missing += 1
            elif NOW - ts(d["pubtime"]) < WEEK:
                young += 1
    print(f"crates: {len(names)} crates, {lines} index lines, without pubtime: {missing}, younger than 7 days: {young}")
    # download URL forms
    req = urllib.request.Request("https://crates.io/api/v1/crates/serde/1.0.200/download", method="HEAD", headers={"User-Agent": "x21-spike"})

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *a, **k):
            return None

    try:
        urllib.request.build_opener(NoRedirect).open(req)
    except urllib.error.HTTPError as e:
        print(f"crates: api download redirects ({e.code}) to {e.headers.get('Location')}")


def homebrew(top=200):
    an = json.loads(get("https://formulae.brew.sh/api/analytics/install-on-request/30d.json"))
    names = [i["formula"] for i in an["items"][: top * 2] if "/" not in i["formula"] and "@" not in i["formula"] or True][:top]

    def one(name):
        try:
            f = json.loads(get(f"https://formulae.brew.sh/api/formula/{urllib.parse.quote(name)}.json"))
            ver = f["versions"]["stable"]
            rev = f.get("revision", 0)
            rebuild = f["bottle"].get("stable", {}).get("rebuild", 0)
            tag = ver + (f"_{rev}" if rev else "") + (f"-{rebuild}" if rebuild else "")
            files = f["bottle"].get("stable", {}).get("files", {})
            pf = files.get("arm64_tahoe") or files.get("arm64_sequoia") or files.get("all")
            if not pf:
                return name, None, "no-bottle"
            repo = urllib.parse.urlparse(pf["url"]).path.split("/blobs/")[0].removeprefix("/v2/")
            tok = json.loads(get(f"https://ghcr.io/token?scope=repository:{repo}:pull"))["token"]
            idx = json.loads(get(f"https://ghcr.io/v2/{repo}/manifests/{tag}", {
                "Authorization": f"Bearer {tok}", "Accept": "application/vnd.oci.image.index.v1+json"}))
            created = idx.get("annotations", {}).get("org.opencontainers.image.created")
            digests = {m["annotations"].get("sh.brew.bottle.digest") for m in idx.get("manifests", [])}
            return name, ts(created) if created else None, "digest-in-index" if pf["sha256"] in digests else "digest-not-in-index"
        except Exception as e:
            return name, None, f"error {e}"

    young = known = 0
    notes = {}
    with cf.ThreadPoolExecutor(16) as ex:
        for name, created, note in ex.map(one, names):
            notes[note] = notes.get(note, 0) + 1
            if note.startswith("error"):
                print("  unresolved:", name, note)
            if created:
                known += 1
                if NOW - created < WEEK:
                    young += 1
    print(f"homebrew: top {len(names)} formulae by 30d installs, bottle created time known for {known}, younger than 7 days: {young} ({100 * young / max(known, 1):.0f}%)")
    print(f"homebrew: notes {notes}")


if __name__ == "__main__":
    {"go": go, "crates": crates, "homebrew": homebrew}[sys.argv[1]]()
