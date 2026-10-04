// Package packscan reads a git pack as it streams from the guest to
// `git receive-pack` and refuses it before git allocates anything large.
//
// Git sizes its buffers from numbers in the pack: index-pack allocates
// the header size of a whole object, and patch_delta allocates the result
// size written at the start of each delta. A pre-receive hook runs only
// after index-pack has done all of that. So this scanner checks those
// numbers first and forwards an entry to git only after it passed.
//
// It parses guest bytes, so it has a fuzz target (FuzzScan).
//
// Throwaway code for X26-pre-receive-check. Not held to the project gates.
package packscan

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Limits are set by the host. Nothing in them comes from the guest.
type Limits struct {
	MaxObjects     uint32 // entries in the pack
	MaxObjectSize  uint64 // inflated size of a whole object, and of a delta's result
	MaxDeltaSize   uint64 // inflated length of the delta data itself
	MaxTotalResult uint64 // sum of all object sizes after delta resolution
}

// Refusal is a policy refusal: the pack is well formed so far but breaks
// a limit. Other errors are malformed packs.
type Refusal struct{ Rule, Detail string }

func (r *Refusal) Error() string { return r.Rule + ": " + r.Detail }

// Stats describe a pack that passed.
type Stats struct {
	Objects     uint32
	Deltas      uint32
	WireBytes   int64
	TotalResult uint64
	Largest     uint64
}

// recorder is the byte source for both the entry parser and zlib. It
// reads from a bufio.Reader, which implements io.ByteReader, so
// compress/flate does not read past the end of each zlib stream, and it
// keeps every byte consumed so the caller can forward exactly those.
type recorder struct {
	r   *bufio.Reader
	buf bytes.Buffer
	h   io.Writer // pack checksum
	n   int64
}

func (c *recorder) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.buf.WriteByte(b)
		c.h.Write([]byte{b})
		c.n++
	}
	return b, err
}

func (c *recorder) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.buf.Write(p[:n])
	c.h.Write(p[:n])
	c.n += int64(n)
	return n, err
}

func (c *recorder) flush(w io.Writer) error {
	_, err := w.Write(c.buf.Bytes())
	c.buf.Reset()
	return err
}

// Scan reads one pack from r, forwarding to w only entries that passed,
// and stops after the trailer. On any error the caller must kill git: w
// then holds a prefix of a pack, which git refuses as truncated.
func Scan(r io.Reader, w io.Writer, lim Limits) (Stats, error) {
	var st Stats
	h := sha1.New()
	rec := &recorder{r: bufio.NewReaderSize(r, 64<<10), h: h}

	var hdr [12]byte
	if _, err := io.ReadFull(rec, hdr[:]); err != nil {
		return st, fmt.Errorf("pack header: %w", err)
	}
	if string(hdr[:4]) != "PACK" {
		return st, errors.New("pack header: bad signature")
	}
	if v := binary.BigEndian.Uint32(hdr[4:8]); v != 2 && v != 3 {
		return st, fmt.Errorf("pack header: version %d", v)
	}
	count := binary.BigEndian.Uint32(hdr[8:12])
	if count > lim.MaxObjects {
		return st, &Refusal{"max-objects", fmt.Sprintf("pack declares %d objects, limit %d", count, lim.MaxObjects)}
	}
	if err := rec.flush(w); err != nil {
		return st, err
	}

	// Result size of each entry by its offset, so an OFS_DELTA's source
	// size can be checked against its base and a chain stays bounded.
	sizes := make(map[int64]uint64, min(count, 1<<16))
	for i := uint32(0); i < count; i++ {
		off := rec.n
		typ, size, err := entryHeader(rec)
		if err != nil {
			return st, fmt.Errorf("entry %d at %d: %w", i, off, err)
		}
		var baseSize uint64
		haveBase := false
		switch typ {
		case 1, 2, 3, 4:
			if size > lim.MaxObjectSize {
				return st, &Refusal{"max-object-size", fmt.Sprintf("entry %d: object of %d bytes, limit %d", i, size, lim.MaxObjectSize)}
			}
		case 6:
			rel, err := ofsDecode(rec)
			if err != nil {
				return st, fmt.Errorf("entry %d: %w", i, err)
			}
			if rel == 0 || rel > uint64(off) {
				return st, fmt.Errorf("entry %d: base offset out of range", i)
			}
			bs, ok := sizes[off-int64(rel)]
			if !ok {
				return st, fmt.Errorf("entry %d: base offset is not an entry", i)
			}
			baseSize, haveBase = bs, true
			st.Deltas++
		case 7:
			var id [20]byte
			if _, err := io.ReadFull(rec, id[:]); err != nil {
				return st, fmt.Errorf("entry %d: base id: %w", i, err)
			}
			// The base is in this pack or in the repository; its size
			// was checked when it arrived, or it is the user's own.
			st.Deltas++
		default:
			return st, fmt.Errorf("entry %d: bad type %d", i, typ)
		}
		if typ >= 6 && size > lim.MaxDeltaSize {
			return st, &Refusal{"max-delta-size", fmt.Sprintf("entry %d: delta of %d bytes, limit %d", i, size, lim.MaxDeltaSize)}
		}

		zr, err := zlib.NewReader(rec)
		if err != nil {
			return st, fmt.Errorf("entry %d: zlib: %w", i, err)
		}
		// Never inflate more than one byte past the declared size, so a
		// zlib bomb costs at most the limit.
		lr := &io.LimitedReader{R: zr, N: int64(size) + 1}
		result := size
		if typ >= 6 {
			br := bufio.NewReaderSize(lr, 32)
			src, err1 := deltaVarint(br)
			dst, err2 := deltaVarint(br)
			if err1 != nil || err2 != nil {
				return st, fmt.Errorf("entry %d: delta header: %v %v", i, err1, err2)
			}
			if haveBase && src != baseSize {
				return st, fmt.Errorf("entry %d: delta source %d, base is %d", i, src, baseSize)
			}
			if dst > lim.MaxObjectSize {
				return st, &Refusal{"max-object-size", fmt.Sprintf("entry %d: delta result of %d bytes, limit %d", i, dst, lim.MaxObjectSize)}
			}
			result = dst
			if _, err := io.Copy(io.Discard, br); err != nil {
				return st, fmt.Errorf("entry %d: inflate: %w", i, err)
			}
		} else if _, err := io.Copy(io.Discard, lr); err != nil {
			return st, fmt.Errorf("entry %d: inflate: %w", i, err)
		}
		if got := int64(size) + 1 - lr.N; got != int64(size) {
			return st, fmt.Errorf("entry %d: inflated %d bytes, header says %d", i, got, size)
		}
		// Consume the zlib trailer (adler32) so the next entry starts
		// where it should.
		if n, err := zr.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			return st, fmt.Errorf("entry %d: zlib stream longer than its header", i)
		}
		_ = zr.Close()

		sizes[off] = result
		st.TotalResult += result
		if result > st.Largest {
			st.Largest = result
		}
		if st.TotalResult > lim.MaxTotalResult {
			return st, &Refusal{"max-total-size", fmt.Sprintf("objects total %d bytes, limit %d", st.TotalResult, lim.MaxTotalResult)}
		}
		st.Objects++
		if err := rec.flush(w); err != nil {
			return st, err
		}
	}

	sum := h.Sum(nil)
	var trailer [20]byte
	if _, err := io.ReadFull(rec.r, trailer[:]); err != nil {
		return st, fmt.Errorf("trailer: %w", err)
	}
	if !bytes.Equal(sum, trailer[:]) {
		return st, errors.New("trailer: checksum mismatch")
	}
	if _, err := w.Write(trailer[:]); err != nil {
		return st, err
	}
	st.WireBytes = rec.n + 20
	// Nothing follows a pack in a push. The caller forwards nothing
	// after the trailer; it does not wait for EOF, because a git client
	// may keep its side open to read the report.
	return st, nil
}

// entryHeader reads git's type-and-size header. Sizes over 2^64 are
// refused rather than wrapped.
func entryHeader(r io.ByteReader) (int, uint64, error) {
	c, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	typ := int(c>>4) & 7
	size := uint64(c & 0x0f)
	shift := uint(4)
	for c&0x80 != 0 {
		if shift > 60 {
			return 0, 0, errors.New("size header too long")
		}
		if c, err = r.ReadByte(); err != nil {
			return 0, 0, err
		}
		size |= uint64(c&0x7f) << shift
		shift += 7
	}
	return typ, size, nil
}

// ofsDecode reads an OFS_DELTA negative offset.
func ofsDecode(r io.ByteReader) (uint64, error) {
	c, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	off := uint64(c & 0x7f)
	for c&0x80 != 0 {
		if off >= 1<<56 {
			return 0, errors.New("base offset overflow")
		}
		if c, err = r.ReadByte(); err != nil {
			return 0, err
		}
		off = ((off + 1) << 7) | uint64(c&0x7f)
	}
	return off, nil
}

func deltaVarint(r io.ByteReader) (uint64, error) {
	var v uint64
	for shift := uint(0); ; shift += 7 {
		if shift > 63 {
			return 0, errors.New("delta size too long")
		}
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, nil
		}
	}
}
