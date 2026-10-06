// Package packgen builds git packfiles byte by byte, including hostile
// ones that git's own tools refuse to write: a blob that inflates to far
// more than it costs on the wire, and a delta whose result is huge.
//
// Throwaway code for X26-pre-receive-check. Not held to the project gates.
package packgen

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

// Object types as they appear in a pack entry header.
const (
	Commit   = 1
	Tree     = 2
	Blob     = 3
	Tag      = 4
	OfsDelta = 6
	RefDelta = 7
)

var typeName = map[int]string{Commit: "commit", Tree: "tree", Blob: "blob", Tag: "tag"}

// Entry is one pack entry. Size is the header size: the inflated size
// for a whole object, the inflated delta length for a delta. Body writes
// the inflated payload; it must write exactly Size bytes for a valid
// entry.
type Entry struct {
	Type    int
	Size    uint64
	Body    func(w io.Writer) error
	BaseIdx int    // OfsDelta: index of the base entry in the pack
	BaseID  []byte // RefDelta: 20-byte object id of the base
}

// Bytes is a Body that writes b.
func Bytes(b []byte) func(io.Writer) error {
	return func(w io.Writer) error { _, err := w.Write(b); return err }
}

// Zeros is a Body that writes n zero bytes without holding them.
func Zeros(n uint64) func(io.Writer) error {
	return func(w io.Writer) error {
		buf := make([]byte, 1<<20)
		for n > 0 {
			k := uint64(len(buf))
			if k > n {
				k = n
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return err
			}
			n -= k
		}
		return nil
	}
}

// Whole is a non-delta entry holding data.
func Whole(typ int, data []byte) Entry {
	return Entry{Type: typ, Size: uint64(len(data)), Body: Bytes(data)}
}

// Write writes a version 2 pack of entries to w, with its SHA-1 trailer.
func Write(w io.Writer, entries []Entry) error {
	h := sha1.New()
	mw := io.MultiWriter(w, h)
	var hdr [12]byte
	copy(hdr[:4], "PACK")
	binary.BigEndian.PutUint32(hdr[4:8], 2)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(entries)))
	if _, err := mw.Write(hdr[:]); err != nil {
		return err
	}
	cw := &countWriter{w: mw, n: 12}
	offs := make([]int64, len(entries))
	for i, e := range entries {
		offs[i] = cw.n
		if _, err := cw.Write(entryHeader(e.Type, e.Size)); err != nil {
			return err
		}
		switch e.Type {
		case OfsDelta:
			if e.BaseIdx < 0 || e.BaseIdx >= i {
				return fmt.Errorf("entry %d: bad base index", i)
			}
			if _, err := cw.Write(ofsEncode(uint64(offs[i] - offs[e.BaseIdx]))); err != nil {
				return err
			}
		case RefDelta:
			if _, err := cw.Write(e.BaseID); err != nil {
				return err
			}
		}
		zw, _ := zlib.NewWriterLevel(cw, zlib.BestCompression)
		if err := e.Body(zw); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
	}
	_, err := w.Write(h.Sum(nil))
	return err
}

// Pack returns the pack as bytes.
func Pack(entries []Entry) []byte {
	var b bytes.Buffer
	if err := Write(&b, entries); err != nil {
		panic(err)
	}
	return b.Bytes()
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func entryHeader(typ int, size uint64) []byte {
	b := []byte{byte(typ<<4) | byte(size&0x0f)}
	size >>= 4
	for size > 0 {
		b[len(b)-1] |= 0x80
		b = append(b, byte(size&0x7f))
		size >>= 7
	}
	return b
}

// ofsEncode is git's offset encoding for OFS_DELTA (big-endian base-128
// with an implicit +1 per continuation byte).
func ofsEncode(off uint64) []byte {
	b := []byte{byte(off & 0x7f)}
	for off >>= 7; off > 0; off >>= 7 {
		off--
		b = append([]byte{byte(0x80 | off&0x7f)}, b...)
	}
	return b
}

// Varint is the size encoding used inside delta data.
func Varint(v uint64) []byte {
	var b []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v > 0 {
			b = append(b, c|0x80)
		} else {
			return append(b, c)
		}
	}
}

// CopyDelta returns delta data that builds a dst-byte result by copying
// the whole src-byte base over and over (copy ops of up to 16 MiB - 1).
// src must be at least 1. The last copy is shortened to fit.
func CopyDelta(src, dst uint64) []byte {
	d := append(Varint(src), Varint(dst)...)
	chunk := src
	if chunk > 0xffffff {
		chunk = 0xffffff
	}
	for left := dst; left > 0; {
		n := chunk
		if n > left {
			n = left
		}
		// copy op: offset 0 (no offset bytes), size in up to 3 bytes.
		op := byte(0x80)
		var sz []byte
		for i := 0; i < 3; i++ {
			if v := byte(n >> (8 * i)); v != 0 {
				op |= 0x10 << i
				sz = append(sz, v)
			}
		}
		d = append(d, op)
		d = append(d, sz...)
		left -= n
	}
	return d
}

// ObjectID is git's SHA-1 object id of an object of type typ.
func ObjectID(typ int, data []byte) []byte {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", typeName[typ], len(data))
	h.Write(data)
	return h.Sum(nil)
}

// Hex formats an id.
func Hex(id []byte) string { return hex.EncodeToString(id) }

// TreeOne is a tree with one blob entry.
func TreeOne(name string, blob []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "100644 %s\x00", name)
	b.Write(blob)
	return b.Bytes()
}

// CommitOf is a commit of tree with an optional parent.
func CommitOf(tree []byte, parent []byte, msg string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "tree %s\n", Hex(tree))
	if parent != nil {
		fmt.Fprintf(&b, "parent %s\n", Hex(parent))
	}
	fmt.Fprintf(&b, "author x26 <x26@example.invalid> 1700000000 +0000\ncommitter x26 <x26@example.invalid> 1700000000 +0000\n\n%s\n", msg)
	return b.Bytes()
}
