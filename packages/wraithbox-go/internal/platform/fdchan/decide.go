package fdchan

import (
	"encoding/binary"
	"math"
)

// controlLayout is how an OS lays out the control data of recvmsg: the
// cmsghdr size and alignment, and the constants a decision reads. The
// decision reads the bytes itself with a layout instead of through the
// syscall package, so the file builds on every OS and the fuzz smoke
// run, which runs on Linux, covers the darwin layout
// (S11-verification-and-spikes, "Fuzzing").
type controlLayout struct {
	hdrLen    int // CMSG_LEN(0): the cmsghdr, already a multiple of align
	align     int // CMSG_ALIGN boundary
	solSocket uint32
	scmRights uint32
	msgTrunc  int
	msgCtrunc int
}

// darwinControl is the layout on darwin, arm64 and amd64 alike: a
// cmsghdr of a uint32 length and two int32s, aligned to 4 bytes. A test
// on darwin checks it against the syscall package.
var darwinControl = controlLayout{
	hdrLen:    12,
	align:     4,
	solSocket: 0xffff,
	scmRights: 1,
	msgTrunc:  0x10,
	msgCtrunc: 0x20,
}

// fdSize is the size of one descriptor in an SCM_RIGHTS payload.
const fdSize = 4

// received is what one recvmsg returned: the bytes read into a data
// buffer one byte larger than the maximum, the control bytes, and the
// flags.
type received struct {
	n       int
	control []byte
	flags   int
}

// decision is what Recv does with a message: accept it, or refuse it
// with a rule. keep is the descriptor that goes to the caller, or -1.
// closeFDs are the descriptors to close: on a refusal, every descriptor
// in the control data. lost counts descriptors the kernel installed that
// the control buffer cut off, which can't be closed.
type decision struct {
	rule     Rule
	keep     int
	closeFDs []int
	lost     int
}

// control is what parseControl found in the control data.
type control struct {
	fds        []int
	cut        bool // a control message runs past the end of the data
	malformed  bool
	unexpected bool
	lost       int
}

// decide is the whole decision about one received message, as a pure
// function (S07-05-stream-hand-off, "Message form"). A message is
// refused when its control data was cut, its record is empty, over max
// or truncated, its control data doesn't parse or holds anything other
// than SCM_RIGHTS, or it carries two or more descriptors. A refusal keeps
// no descriptor and closes every one it found. Its inputs come from the
// peer, which may be compromised.
func decide(lay controlLayout, r received, maxRecord int) decision {
	c := parseControl(lay, r.control)
	d := decision{keep: -1, lost: c.lost}
	switch {
	case r.flags&lay.msgCtrunc != 0 || c.cut:
		d.rule = RuleControlTruncated
	case r.n < 1 || r.n > maxRecord || r.flags&lay.msgTrunc != 0:
		d.rule = RuleRecordSize
	case c.malformed:
		d.rule = RuleControlMalformed
	case c.unexpected:
		d.rule = RuleControlUnexpected
	case len(c.fds) > 1:
		d.rule = RuleFDCount
	case len(c.fds) == 1:
		d.keep = c.fds[0]
		return d
	default:
		return d
	}
	d.closeFDs = c.fds
	return d
}

// parseControl walks the control messages as CMSG_FIRSTHDR and
// CMSG_NXTHDR do, and collects the descriptor of every SCM_RIGHTS
// message, also of one that is cut off or malformed, so that the caller
// can close each one.
func parseControl(lay controlLayout, b []byte) control {
	var c control
	for off := 0; off < len(b); {
		rest := b[off:]
		if len(rest) < lay.hdrLen {
			c.malformed = true
			return c
		}
		msgLen := int(binary.NativeEndian.Uint32(rest))
		level := binary.NativeEndian.Uint32(rest[4:])
		typ := binary.NativeEndian.Uint32(rest[8:])
		if msgLen < lay.hdrLen {
			c.malformed = true
			return c
		}
		end := msgLen
		if end > len(rest) {
			c.cut = true
			end = len(rest)
		}
		data := rest[lay.hdrLen:end]
		if level != lay.solSocket || typ != lay.scmRights {
			c.unexpected = true
		} else {
			c.collect(data)
			if msgLen > len(rest) {
				c.lost += (msgLen - lay.hdrLen - len(data)) / fdSize
			}
		}
		if msgLen > len(rest) {
			return c
		}
		off += alignUp(msgLen, lay.align)
	}
	return c
}

// collect appends the descriptors of an SCM_RIGHTS payload. A payload
// that isn't a whole number of descriptors, or holds a negative one, is
// malformed.
func (c *control) collect(data []byte) {
	if len(data)%fdSize != 0 {
		c.malformed = true
	}
	for i := 0; i+fdSize <= len(data); i += fdSize {
		fd := binary.NativeEndian.Uint32(data[i:])
		if fd > math.MaxInt32 {
			// A negative int.
			c.malformed = true
			continue
		}
		c.fds = append(c.fds, int(fd))
	}
}

func alignUp(n, align int) int {
	return (n + align - 1) &^ (align - 1)
}
