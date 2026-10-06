// X28-git-alloc-limit: the X26 bomb packs again, with GIT_ALLOC_LIMIT in
// receive-pack's environment. Does git refuse each oversized allocation
// before it makes it, and does that bound the memory of a push without
// the pack scanner?
//
// Throwaway code. Not held to the project gates.
package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"x26/packgen"
	"x26/pktfilter"
)

const allocLimit = "100m" // 104857600 bytes, git_env_ulong's unit suffix

var only = flag.String("only", "", "alloc phase: run only bombs whose name contains this")

// withCount rewrites the object count in a pack's header and its
// trailing checksum, so the header declares n objects while the pack
// holds what it holds.
func withCount(pack []byte, n uint32) []byte {
	p := append([]byte(nil), pack[:len(pack)-20]...)
	binary.BigEndian.PutUint32(p[8:12], n)
	sum := sha1.Sum(p)
	return append(p, sum[:]...)
}

// tmpObjdirs counts quarantine directories left in a landing repository.
func tmpObjdirs(landing string) int {
	m, _ := filepath.Glob(filepath.Join(landing, "objects", "tmp_objdir-*"))
	return len(m)
}

type allocBomb struct {
	name  string
	pack  func() ([]byte, string)
	setup func(h *H, landing string) // optional: prepare the landing repository
	modes []int                      // run only these modes (indexes); nil = all
	limit string                     // GIT_ALLOC_LIMIT for the limit modes, if not allocLimit
}

func allocBombs(h *H) []allocBomb {
	var bs []allocBomb
	for mib := 256; mib <= *maxBomb; mib *= 2 {
		n := uint64(mib) << 20
		bs = append(bs, allocBomb{name: fmt.Sprintf("One delta, result %d MiB", mib), pack: func() ([]byte, string) { return deltaBombs(8<<20, []uint64{n}) }})
	}
	many := func(k int, size uint64) func() ([]byte, string) {
		return func() ([]byte, string) {
			var rs []uint64
			for i := 0; i < k; i++ {
				rs = append(rs, size-uint64(i))
			}
			return deltaBombs(8<<20, rs)
		}
	}
	bs = append(bs,
		allocBomb{name: "8 deltas of 256 MiB", pack: many(8, 256<<20)},
		allocBomb{name: "8 deltas of 96 MiB", pack: many(8, 96<<20)},
		allocBomb{name: "64 deltas of 96 MiB (6 GiB together)", pack: many(64, 96<<20)},
		allocBomb{name: "Chain of 8 deltas of 96 MiB", pack: func() ([]byte, string) {
			entries := []packgen.Entry{{Type: packgen.Blob, Size: 8 << 20, Body: packgen.Zeros(8 << 20)}}
			sizes := []uint64{8 << 20}
			prev := uint64(8 << 20)
			for i := 0; i < 8; i++ {
				r := 96<<20 - uint64(i)
				d := packgen.CopyDelta(prev, r)
				entries = append(entries, packgen.Entry{Type: packgen.OfsDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseIdx: len(entries) - 1})
				sizes = append(sizes, r)
				prev = r
			}
			return blobPush(sizes, entries)
		}},
		allocBomb{name: "One delta, result 99 MiB (under the limit)", pack: func() ([]byte, string) { return deltaBombs(8<<20, []uint64{99 << 20}) }},
		allocBomb{name: "One delta, result exactly 100 MiB", pack: func() ([]byte, string) { return deltaBombs(8<<20, []uint64{100 << 20}) }},
		// git's xmallocz adds one byte for a terminating NUL, so a
		// limit of cap+1 lets an object of exactly the cap through.
		allocBomb{name: "One delta, result exactly 100 MiB, GIT_ALLOC_LIMIT=104857601", limit: "104857601", pack: func() ([]byte, string) { return deltaBombs(8<<20, []uint64{100 << 20}) }},
		allocBomb{name: "One delta, result 100 MiB + 1, GIT_ALLOC_LIMIT=104857601", limit: "104857601", pack: func() ([]byte, string) { return deltaBombs(8<<20, []uint64{100<<20 + 1}) }},
		allocBomb{name: "Delta data of 128 MiB (copy ops of 1 byte, result 64 MiB)", pack: func() ([]byte, string) {
			// A delta whose own inflated data is large: many 1-byte
			// inserts would be slow to build, so use copy ops of 1 byte
			// from offset 0 (4 bytes each with offset 0 and size 1:
			// 0x90 0x01) until the delta data passes 128 MiB.
			base := uint64(8 << 20)
			n := uint64(128<<20) / 2
			if n > 99<<20 {
				n = 99 << 20
			}
			d := append(packgen.Varint(base), packgen.Varint(n)...)
			op := []byte{0x90, 0x01}
			d = append(d, bytes.Repeat(op, int(n))...)
			entries := []packgen.Entry{{Type: packgen.Blob, Size: base, Body: packgen.Zeros(base)}, {Type: packgen.OfsDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseIdx: 0}}
			return blobPush([]uint64{base, n}, entries)
		}},
		allocBomb{name: "REF_DELTA against a 200 MiB blob in the borrowed repository, result 1 KiB", setup: setupBigBase, pack: refDeltaBigBase},
		allocBomb{name: "Blob of 99 MiB zeros", pack: func() ([]byte, string) { return zlibBomb(99 << 20) }},
		allocBomb{name: "Blob of 400 MiB zeros", pack: func() ([]byte, string) { return zlibBomb(400 << 20) }},
		allocBomb{name: "Blob of 1 GiB zeros", pack: func() ([]byte, string) { return zlibBomb(1 << 30) }},
	)
	// index-pack callocs (count+1) * sizeof(struct object_entry), 64 bytes
	// on arm64, before it reads an entry. Under 104857600 that allows
	// counts up to 1,638,399. The pack ends after its 3 entries, without a
	// checksum, so git fails on EOF unless the limit stops it first.
	for _, n := range []uint32{1_000_000, 1_638_399, 1_638_400, 10_000_000, 100_000_000, 0xffffffff} {
		bs = append(bs, allocBomb{name: fmt.Sprintf("Header declares %d objects, pack holds 3, no checksum", n), modes: []int{0, 1}, pack: func() ([]byte, string) {
			c := newCommit("", fmt.Sprintf("count-%d", n))
			p := withCount(packgen.Pack(c.entries), n)
			return p[:len(p)-20], c.id
		}})
	}
	bs = append(bs, allocBomb{name: "1,000,000 tiny blobs no ref reaches, and one commit", modes: []int{0, 1}, pack: func() ([]byte, string) {
		c := newCommit("", "tiny")
		entries := c.entries
		for i := 0; i < 1_000_000; i++ {
			entries = append(entries, packgen.Whole(packgen.Blob, []byte{byte(i), byte(i >> 8), byte(i >> 16)}))
		}
		return packgen.Pack(entries), c.id
	}})
	bs = append(bs, allocBomb{name: "640 deltas of 96 MiB (60 GiB together)", pack: many(640, 96<<20), modes: []int{1}})
	return bs
}

const bigBase = 200 << 20

// setupBigBase gives the landing repository an alternate that holds a
// blob of bigBase zeros, as export.git would hold a large file of the
// user's repository.
func setupBigBase(h *H, landing string) {
	exp := filepath.Join(h.root, "bigbase.git")
	if _, err := os.Stat(exp); err != nil {
		h.git(h.root, "init", "-q", "--bare", exp)
		f := filepath.Join(h.root, "bigbase.bin")
		must(os.WriteFile(f, make([]byte, bigBase), 0o644))
		h.git(exp, "hash-object", "-w", f)
		_ = os.Remove(f)
	}
	must(os.WriteFile(filepath.Join(landing, "objects", "info", "alternates"), []byte(filepath.Join(exp, "objects")+"\n"), 0o644))
}

func refDeltaBigBase() ([]byte, string) {
	base := zeroBlobID(bigBase)
	d := packgen.CopyDelta(bigBase, 1024)
	// The thin pack names only the delta's result in its tree.
	res := zeroBlobID(1024)
	var tree bytes.Buffer
	fmt.Fprintf(&tree, "100644 small\x00")
	tree.Write(res)
	tid := packgen.ObjectID(packgen.Tree, tree.Bytes())
	c := packgen.CommitOf(tid, nil, "thin")
	entries := []packgen.Entry{
		{Type: packgen.RefDelta, Size: uint64(len(d)), Body: packgen.Bytes(d), BaseID: base},
		packgen.Whole(packgen.Tree, tree.Bytes()),
		packgen.Whole(packgen.Commit, c),
	}
	return packgen.Pack(entries), packgen.Hex(packgen.ObjectID(packgen.Commit, c))
}

func (h *H) phaseAlloc() {
	fmt.Printf("\n## GIT_ALLOC_LIMIT=%s\n\nmaxrss is ru_maxrss of receive-pack with its reaped children; cpu is their user + system time. \"S08\" is receive.unpackLimit=1 and pack.threads=1 with the pre-receive check, as S08-workspace-and-git sets them. \"Left\" is what the push added to objects/ of landing.git, and the quarantine directories it left.\n\n", allocLimit)
	fmt.Printf("| Pack | Wire bytes | Mode | Result | Time | cpu | maxrss | Left |\n|---|---|---|---|---|---|---|---|\n")
	modes := []struct {
		name string
		o    opts
	}{
		{"S08, no limit", opts{hook: true, unpackLimit: 1, threads: 1}},
		{"S08 + limit", opts{hook: true, unpackLimit: 1, threads: 1, allocLimit: allocLimit}},
		{"S08 + limit, no hook", opts{unpackLimit: 1, threads: 1, allocLimit: allocLimit}},
		{"git defaults (unpack-objects under 100 objects) + limit, no hook", opts{allocLimit: allocLimit}},
		{"S08, no limit, pack without its checksum", opts{hook: true, unpackLimit: 1, threads: 1, noTrailer: true}},
	}
	for _, b := range allocBombs(h) {
		if *only != "" && !strings.Contains(b.name, *only) {
			continue
		}
		pack, head := b.pack()
		for mi, m := range modes {
			if b.modes != nil && !slices.Contains(b.modes, mi) {
				continue
			}
			if b.limit != "" && m.o.allocLimit != "" {
				m.o.allocLimit = b.limit
			}
			landing, _ := h.newLanding(fmt.Sprintf("alloc-%d", time.Now().UnixNano()), "")
			if b.setup != nil {
				b.setup(h, landing)
			}
			before := landingObjects(landing)
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: head, Ref: prefix + "bomb"}}, pack, m.o)
			after := landingObjects(landing)
			res := strings.TrimSpace(r.report)
			if res == "" {
				res = fmt.Sprintf("err=%v", r.err)
			}
			if s := strings.TrimSpace(r.stderr); s != "" {
				res += " / stderr: " + tail(strings.ReplaceAll(s, "\n", " / "), 200)
			}
			left := fmt.Sprintf("+%d B, %d quarantine", after.total()-before.total(), tmpObjdirs(landing))
			fmt.Printf("| %s | %d | %s | %s | %.2f s | %.2f s | %d MiB | %s |\n", b.name, len(pack), m.name, esc(res), r.dur.Seconds(), r.cpu.Seconds(), r.maxRSS>>20, left)
			_ = os.RemoveAll(landing)
		}
	}
}

// phaseAllocCost checks that the limit doesn't refuse real pushes: the
// Go repository's whole history into an empty landing repository, and
// one new commit into a landing repository that borrows from it.
func (h *H) phaseAllocCost() {
	if *bigRepo == "" {
		fmt.Printf("\n## Cost with the limit\n\nskipped: no -big repository\n")
		return
	}
	head := h.git(*bigRepo, "rev-parse", "master")
	fmt.Printf("\n## Cost with the limit\n\nLarge repository: %s, master at %s.\n\n", *bigRepo, head[:10])
	pf := filepath.Join(h.root, "whole.pack")
	cmd := exec.Command(*gitBin, "pack-objects", "--stdout", "--revs", "-q")
	cmd.Dir = *bigRepo
	cmd.Env = h.env()
	cmd.Stdin = strings.NewReader("master\n")
	f, err := os.Create(pf)
	must(err)
	cmd.Stdout = f
	must(cmd.Run())
	f.Close()
	pack, err := os.ReadFile(pf)
	must(err)
	fmt.Printf("Whole-history pack: %d MiB, %d objects in its header.\n\n", len(pack)>>20, binary.BigEndian.Uint32(pack[8:12]))
	fmt.Printf("| Step | Result | Median (range) | cpu (last) | maxrss | Runs |\n|---|---|---|---|---|---|\n")
	for _, m := range []struct {
		name string
		o    opts
	}{
		{"S08, no limit", opts{hook: true, unpackLimit: 1, threads: 1}},
		{"S08 + limit", opts{hook: true, unpackLimit: 1, threads: 1, allocLimit: allocLimit}},
	} {
		m.o.maxInput = 1 << 30
		m.o.maxTotal = 1 << 40
		var ts []time.Duration
		var rss int64
		var rep string
		var cpu time.Duration
		for i := 0; i < *costRuns; i++ {
			landing := filepath.Join(h.root, fmt.Sprintf("cost-%d.git", time.Now().UnixNano()))
			h.git(h.root, "init", "-q", "--bare", landing)
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: head, Ref: prefix + "whole"}}, pack, m.o)
			ts = append(ts, r.dur)
			if r.maxRSS > rss {
				rss = r.maxRSS
			}
			cpu = r.cpu
			rep = strings.TrimSpace(r.report)
			if rep == "" || !strings.Contains(rep, "ok ") {
				rep += " / " + tail(strings.ReplaceAll(strings.TrimSpace(r.stderr), "\n", " / "), 200)
			}
			_ = os.RemoveAll(landing)
		}
		fmt.Printf("| Whole history, %s | %s | %s | %.1f s | %d MiB | %d |\n", m.name, esc(tail(rep, 220)), med(ts), cpu.Seconds(), rss>>20, len(ts))
	}
	landing := filepath.Join(h.root, "borrow.git")
	h.git(h.root, "init", "-q", "--bare", landing)
	must(os.WriteFile(filepath.Join(landing, "objects", "info", "alternates"), []byte(filepath.Join(*bigRepo, "objects")+"\n"), 0o644))
	for _, m := range []struct {
		name string
		o    opts
	}{
		{"S08, no limit", opts{hook: true, unpackLimit: 1, threads: 1}},
		{"S08 + limit", opts{hook: true, unpackLimit: 1, threads: 1, allocLimit: allocLimit}},
	} {
		var ts []time.Duration
		var rep string
		var rss int64
		for i := 0; i < *costRuns; i++ {
			c := newCommit(head, fmt.Sprintf("small-%s-%d", m.name, i))
			r := h.receive(landing, []pktfilter.Command{{Old: zero, New: c.id, Ref: fmt.Sprintf("%ssmall-%d-%d", prefix, len(m.name), i)}}, packgen.Pack(c.entries), m.o)
			ts = append(ts, r.dur)
			rep = strings.TrimSpace(r.report)
			if r.maxRSS > rss {
				rss = r.maxRSS
			}
		}
		fmt.Printf("| One new commit, landing borrows from the large repository, %s | %s | %s | - | %d MiB | %d |\n", m.name, esc(tail(rep, 80)), med(ts), rss>>20, len(ts))
	}
}
