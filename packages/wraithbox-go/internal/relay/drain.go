package relay

// maxReplyParams is the most parameter bytes of a reply the drain
// takes for the reply to its sentinel. A real one is `CSI ? r ; c R` or
// `CSI ? r ; c ; p R`, with numbers of at most 5 digits each.
const maxReplyParams = 32

// drainState is where the drain scanner is in a candidate reply.
type drainState uint8

const (
	drainGround drainState = iota
	drainESC
	drainCSI
	drainParams
)

// drainScanner finds the reply to the drain's sentinel query in
// terminal input: `ESC [ ?`, 1 to maxReplyParams digits and `;`, then
// `R` (S16-03-around-the-stream, "Input drain"). Terminals answer in
// order, so every reply to a guest query comes before it. A cursor
// position report without `?`, the reply to a guest's `CSI 6 n`, never
// ends the drain. The input can hold replies to guest queries, which is
// guest-influenced, so FuzzDrainReply covers the scanner. It keeps no
// bytes, only its state, so it reads any amount of input in constant
// memory.
type drainScanner struct {
	state  drainState
	params int
}

// feed scans p and reports whether it holds the end of the reply, and
// where: the index just past its `R`. Bytes after it aren't read.
func (d *drainScanner) feed(p []byte) (int, bool) {
	for i, b := range p {
		if d.step(b) {
			return i + 1, true
		}
	}
	return len(p), false
}

func (d *drainScanner) step(b byte) bool {
	if b == 0x1b {
		d.state, d.params = drainESC, 0
		return false
	}
	switch d.state {
	case drainESC:
		d.state = next(b == '[', drainCSI)
	case drainCSI:
		d.state = next(b == '?', drainParams)
	case drainParams:
		return d.param(b)
	case drainGround:
	}
	return false
}

// next returns to when ok, and to the ground state otherwise.
func next(ok bool, to drainState) drainState {
	if ok {
		return to
	}
	return drainGround
}

func (d *drainScanner) param(b byte) bool {
	switch {
	case (b >= '0' && b <= '9') || b == ';':
		d.params++
		if d.params > maxReplyParams {
			d.state = drainGround
		}
		return false
	case b == 'R':
		d.state = drainGround
		return d.params > 0
	}
	d.state = drainGround
	return false
}
