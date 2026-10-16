package fdchan

import (
	"encoding/binary"
	"slices"
	"testing"
)

// cmsg builds one darwin control message with the given level, type and
// payload, padded to the next alignment boundary.
func cmsg(level, typ uint32, payload []byte) []byte {
	lay := darwinControl
	b := make([]byte, lay.hdrLen, alignUp(lay.hdrLen+len(payload), lay.align))
	binary.NativeEndian.PutUint32(b, uint32(lay.hdrLen+len(payload)))
	binary.NativeEndian.PutUint32(b[4:], level)
	binary.NativeEndian.PutUint32(b[8:], typ)
	b = append(b, payload...)
	return b[:cap(b)]
}

// header builds a bare darwin cmsghdr with any length.
func header(msgLen int, level, typ uint32) []byte {
	b := binary.NativeEndian.AppendUint32(nil, uint32(msgLen))
	b = binary.NativeEndian.AppendUint32(b, level)
	return binary.NativeEndian.AppendUint32(b, typ)
}

// rights builds an SCM_RIGHTS control message with the given descriptors.
func rights(fds ...int) []byte {
	payload := make([]byte, 0, len(fds)*fdSize)
	for _, fd := range fds {
		payload = binary.NativeEndian.AppendUint32(payload, uint32(int32(fd)))
	}
	return cmsg(darwinControl.solSocket, darwinControl.scmRights, payload)
}

// TestDecide feeds the decision built control bytes and flags, and sees
// each refusal with its rule and every descriptor in the close list
// (S07-05-stream-hand-off, "Message form").
func TestDecide(t *testing.T) {
	const maxRecord = 4096
	lay := darwinControl
	const scmCreds = 3 // SCM_CREDS on darwin
	tests := []struct {
		name  string
		r     received
		rule  Rule
		keep  int
		close []int
		lost  int
	}{
		{name: "record only", r: received{n: 10}, keep: -1},
		{name: "record of the maximum with one descriptor", r: received{n: maxRecord, control: rights(7)}, keep: 7},
		{name: "empty record", r: received{n: 0, control: rights(7)}, rule: RuleRecordSize, keep: -1, close: []int{7}},
		{name: "record one byte over", r: received{n: maxRecord + 1, control: rights(7)}, rule: RuleRecordSize, keep: -1, close: []int{7}},
		{name: "MSG_TRUNC", r: received{n: 10, flags: lay.msgTrunc, control: rights(7)}, rule: RuleRecordSize, keep: -1, close: []int{7}},
		{name: "MSG_CTRUNC", r: received{n: 10, flags: lay.msgCtrunc, control: rights(7, 8)}, rule: RuleControlTruncated, keep: -1, close: []int{7, 8}},
		{
			name: "control cut inside a message",
			r:    received{n: 10, control: rights(7, 8, 9)[:lay.hdrLen+2*fdSize]},
			rule: RuleControlTruncated, keep: -1, close: []int{7, 8}, lost: 1,
		},
		{name: "two descriptors", r: received{n: 10, control: rights(7, 8)}, rule: RuleFDCount, keep: -1, close: []int{7, 8}},
		{name: "three descriptors", r: received{n: 10, control: rights(7, 8, 9)}, rule: RuleFDCount, keep: -1, close: []int{7, 8, 9}},
		{
			name: "two SCM_RIGHTS messages",
			r:    received{n: 10, control: slices.Concat(rights(7), rights(8))},
			rule: RuleFDCount, keep: -1, close: []int{7, 8},
		},
		{
			name: "SCM_CREDS",
			r:    received{n: 10, control: cmsg(lay.solSocket, scmCreds, make([]byte, 8))},
			rule: RuleControlUnexpected, keep: -1,
		},
		{
			name: "SCM_CREDS beside SCM_RIGHTS",
			r:    received{n: 10, control: slices.Concat(cmsg(lay.solSocket, scmCreds, make([]byte, 8)), rights(7))},
			rule: RuleControlUnexpected, keep: -1, close: []int{7},
		},
		{
			name: "another level",
			r:    received{n: 10, control: cmsg(0, lay.scmRights, binary.NativeEndian.AppendUint32(nil, 7))},
			rule: RuleControlUnexpected, keep: -1,
		},
		{name: "header shorter than a header", r: received{n: 10, control: slices.Concat(rights(7), make([]byte, 5))}, rule: RuleControlMalformed, keep: -1, close: []int{7}},
		{
			name: "length below the header",
			r:    received{n: 10, control: slices.Concat(rights(7), header(lay.hdrLen-1, lay.solSocket, lay.scmRights))},
			rule: RuleControlMalformed, keep: -1, close: []int{7},
		},
		{
			name: "partial descriptor",
			r:    received{n: 10, control: cmsg(lay.solSocket, lay.scmRights, []byte{7, 0, 0, 0, 1, 2})},
			rule: RuleControlMalformed, keep: -1, close: []int{7},
		},
		{name: "negative descriptor", r: received{n: 10, control: rights(-1)}, rule: RuleControlMalformed, keep: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decide(lay, tt.r, maxRecord)
			if d.rule != tt.rule || d.keep != tt.keep || !slices.Equal(d.closeFDs, tt.close) || d.lost != tt.lost {
				t.Errorf("decide = %+v, want rule %q keep %d close %v lost %d", d, tt.rule, tt.keep, tt.close, tt.lost)
			}
		})
	}
}

// referenceFDs is a second, plain walk over darwin control bytes that
// lists the non-negative descriptor of every SCM_RIGHTS message within
// the bytes, for the fuzz property.
func referenceFDs(b []byte) []int {
	lay := darwinControl
	var fds []int
	for off := 0; off+lay.hdrLen <= len(b); {
		msgLen := int(binary.NativeEndian.Uint32(b[off:]))
		level := binary.NativeEndian.Uint32(b[off+4:])
		typ := binary.NativeEndian.Uint32(b[off+8:])
		if msgLen < lay.hdrLen {
			break
		}
		end := min(off+msgLen, len(b))
		if level == lay.solSocket && typ == lay.scmRights {
			for i := off + lay.hdrLen; i+fdSize <= end; i += fdSize {
				if fd := int(int32(binary.NativeEndian.Uint32(b[i:]))); fd >= 0 {
					fds = append(fds, fd)
				}
			}
		}
		off += alignUp(msgLen, lay.align)
	}
	return fds
}

// FuzzRecvDecision runs the decision on arbitrary bytes read, control
// bytes, flags and maximum. A compromised wb-netd or wb-vmd writes these
// messages. Properties: it never panics, a refusal keeps no descriptor,
// an accepted message has at most one and passes every check, and every
// descriptor of every SCM_RIGHTS message is kept or closed.
func FuzzRecvDecision(f *testing.F) {
	lay := darwinControl
	f.Add(10, 0, 4096, []byte(nil))
	f.Add(4096, 0, 4096, rights(7))
	f.Add(10, 0, 4096, rights(7, 8))
	f.Add(10, 0, 4096, slices.Concat(rights(7), rights(8)))
	f.Add(10, lay.msgCtrunc, 4096, rights(7, 8))
	f.Add(4097, lay.msgTrunc, 4096, rights(7))
	f.Add(10, 0, 4096, cmsg(lay.solSocket, 3, make([]byte, 8)))
	f.Add(10, 0, 4096, rights(7, 8, 9)[:20])
	f.Fuzz(func(t *testing.T, n, flags, maxRecord int, control []byte) {
		d := decide(lay, received{n: n, control: control, flags: flags}, maxRecord)
		want := referenceFDs(control)
		if d.rule != "" {
			if d.keep != -1 {
				t.Fatalf("refused with %s but kept %d", d.rule, d.keep)
			}
			if !slices.Equal(d.closeFDs, want) {
				t.Fatalf("refused with %s, closes %v, want %v", d.rule, d.closeFDs, want)
			}
			return
		}
		if n < 1 || n > maxRecord || flags&(lay.msgTrunc|lay.msgCtrunc) != 0 || len(d.closeFDs) != 0 {
			t.Fatalf("accepted n=%d max=%d flags=%#x close=%v", n, maxRecord, flags, d.closeFDs)
		}
		switch {
		case len(want) == 0 && d.keep == -1:
		case len(want) == 1 && d.keep == want[0]:
		default:
			t.Fatalf("accepted keeping %d, control holds %v", d.keep, want)
		}
	})
}
