package egressmsg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// MaxPayload is the largest payload on a payload pipe (control.proto).
// A test builds the largest NetdStartPayload and EffectivePolicy the
// bounds allow and checks that each fits.
const MaxPayload = 64 << 20

// PayloadDeadline bounds the read of a whole payload.
const PayloadDeadline = 10 * time.Second

// payloadPrefix is the size of a payload's length.
const payloadPrefix = 4

// WritePayload writes m to w as a payload: its length as 4 big-endian
// bytes, then its encoding. The caller closes w after it, so the reader
// sees end of file.
func WritePayload(w io.Writer, m proto.Message) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("egressmsg: %w", err)
	}
	if len(b) > MaxPayload {
		return fmt.Errorf("egressmsg: a payload of %d bytes, the cap is %d", len(b), MaxPayload)
	}
	buf := make([]byte, payloadPrefix, payloadPrefix+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b))) //nolint:gosec // G115: len(b) is at most MaxPayload, checked above
	if _, err := w.Write(append(buf, b...)); err != nil {
		return fmt.Errorf("egressmsg: write payload: %w", err)
	}
	return nil
}

// ReadPayload reads one payload from f, the descriptor a control
// message carried, into m. It refuses, with CheckPayload, a descriptor
// that isn't a pipe or can't take a read deadline, a length over
// MaxPayload, a payload that isn't whole within PayloadDeadline, and a
// byte after it. Then it refuses what decodePayload refuses. It doesn't
// close f.
func ReadPayload(f *os.File, m proto.Message) *Refusal {
	return readPipe(f, m, time.Now().Add(PayloadDeadline))
}

// readPipe is ReadPayload with the deadline as a parameter, for a test.
func readPipe(f *os.File, m proto.Message, deadline time.Time) *Refusal {
	st, err := f.Stat()
	if err != nil {
		return &Refusal{Check: CheckPayload, msg: "the descriptor can't be read", err: err}
	}
	if st.Mode()&os.ModeNamedPipe == 0 {
		return refuse(CheckPayload, "", "the descriptor isn't a pipe")
	}
	if err := f.SetReadDeadline(deadline); err != nil {
		return &Refusal{Check: CheckPayload, msg: "the pipe takes no read deadline", err: err}
	}
	return readPayload(f, m)
}

// readPayload reads one payload from r into m: the length, that many
// bytes, and then end of file. It holds no more than MaxPayload bytes.
func readPayload(r io.Reader, m proto.Message) *Refusal {
	var prefix [payloadPrefix]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return &Refusal{Check: CheckPayload, msg: "the length isn't whole", err: err}
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > MaxPayload {
		return refuse(CheckPayload, "", fmt.Sprintf("a length of %d bytes, the cap is %d", n, MaxPayload))
	}
	// ReadAll grows its buffer as the bytes come, so a length the
	// writer doesn't fill holds no more memory than the bytes it wrote.
	b, err := io.ReadAll(io.LimitReader(r, int64(n)))
	if err != nil || len(b) != int(n) {
		return &Refusal{Check: CheckPayload, msg: fmt.Sprintf("the payload of %d bytes isn't whole", n), err: err}
	}
	var one [1]byte
	switch k, err := r.Read(one[:]); {
	case k > 0:
		return refuse(CheckPayload, "", "bytes follow the payload")
	case !errors.Is(err, io.EOF):
		return &Refusal{Check: CheckPayload, msg: "no end of file after the payload", err: err}
	}
	return unmarshal(b, m)
}

// CheckNetdStart checks a NetdStartPayload a wb-netd read: at most
// MaxEntries entries, each as a MappingEntry accounting record, no
// address twice; at most MaxWildcardNames debited names, each in
// canonical form, none twice; and a debit count of at most
// MaxWildcardNames.
func CheckNetdStart(p *egressv1.NetdStartPayload) *Refusal {
	if len(p.GetEntries()) > MaxEntries {
		return refuse(CheckMessageForm, "entries", fmt.Sprintf("%d entries, the most is %d", len(p.GetEntries()), MaxEntries))
	}
	addrs := make(map[uint32]bool, len(p.GetEntries()))
	for _, e := range p.GetEntries() {
		if _, r := mappingEntry(e); r != nil {
			return r
		}
		if addrs[e.GetAddress()] {
			return refuse(CheckMessageForm, "entries", "an address twice")
		}
		addrs[e.GetAddress()] = true
	}
	if r := names("debited", p.GetDebited()); r != nil {
		return r
	}
	if p.GetDebitCount() > MaxWildcardNames {
		return refuse(CheckMessageForm, "debit_count", fmt.Sprintf("%d, the most is %d", p.GetDebitCount(), MaxWildcardNames))
	}
	return nil
}

// CheckProxydStart checks a ProxydStartPayload wb-proxyd read: each set
// for a VM slot, no slot twice, and each set's names as in
// CheckNetdStart's debited names.
func CheckProxydStart(p *egressv1.ProxydStartPayload) *Refusal {
	seen := map[string]bool{}
	for _, set := range p.GetWildcardNames() {
		vm, r := VM("wildcard_names.vm", set.GetVm())
		if r != nil {
			return r
		}
		if seen[vm] {
			return refuse(CheckMessageForm, "wildcard_names", "a VM twice")
		}
		seen[vm] = true
		if r := names("wildcard_names.names", set.GetNames()); r != nil {
			return r
		}
	}
	return nil
}

// names checks a list of names: at most MaxWildcardNames, each in
// canonical form, none twice.
func names(field string, list []string) *Refusal {
	if len(list) > MaxWildcardNames {
		return refuse(CheckMessageForm, field, fmt.Sprintf("%d names, the most is %d", len(list), MaxWildcardNames))
	}
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		if _, r := Name(field, s); r != nil {
			return r
		}
		if seen[s] {
			return refuse(CheckMessageForm, field, "a name twice")
		}
		seen[s] = true
	}
	return nil
}
