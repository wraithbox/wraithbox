package dns

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// MaxQuery is the largest query accepted, in bytes: the DNS message size
// over UDP without EDNS (RFC 1035, section 4.2.1). A query with one
// question and an OPT record fits in it. A larger one is dropped with
// dns-message-form (S07-03-dns, "Messages").
const MaxQuery = 512

// The record types and the class the server reads and writes.
const (
	TypeA     uint16 = 1
	TypeSOA   uint16 = 6
	TypeTXT   uint16 = 16
	TypeAAAA  uint16 = 28
	TypeOPT   uint16 = 41
	TypeSVCB  uint16 = 64
	TypeHTTPS uint16 = 65
	ClassIN   uint16 = 1
)

// The response codes the server sends.
const (
	rcodeNoError  = 0
	rcodeServFail = 2
	rcodeNXDomain = 3
	rcodeRefused  = 5
)

// The header's flag bits (RFC 1035, section 4.1.1).
const (
	flagQR      = 0x8000
	flagRD      = 0x0100
	flagRA      = 0x0080
	opcodeMask  = 0x7800
	headerLen   = 12
	labelTypes  = 0xc0
	maxLogBytes = 300
)

// Query is a query that passed the message form: one question of class
// IN. It owns its bytes, so a held query keeps them.
type Query struct {
	// ID and Flags are the header's.
	ID    uint16
	Flags uint16
	// Labels are the question's labels as they are on the wire, without
	// the root label. Nothing has checked them against the name form.
	Labels [][]byte
	// Type is the question's record type.
	Type uint16
	// question is the question's wire bytes: its name, type and class.
	// Each response echoes them, so the case the guest sent comes back.
	question []byte
}

// The reasons a message fails the form, logged with dns-message-form.
var (
	errShort      = errors.New("shorter than a header")
	errLong       = errors.New("longer than the largest query")
	errResponse   = errors.New("a response, not a query")
	errOpcode     = errors.New("an opcode other than QUERY")
	errQuestions  = errors.New("not exactly one question")
	errRecords    = errors.New("answer or authority records")
	errAdditional = errors.New("more than one additional record")
	errTruncated  = errors.New("a record runs past the message")
	errPointer    = errors.New("a compression pointer in the question")
	errLabelType  = errors.New("a label type other than a plain label")
	errClass      = errors.New("a class other than IN")
	errNotOPT     = errors.New("an additional record that isn't an OPT for the root")
	errTrailing   = errors.New("bytes after the last record")
)

// ParseQuery reads a message from the guest (S07-03-dns, "Messages"). It
// accepts only a query (QR clear, opcode QUERY) with exactly one question
// of class IN, no answer or authority records, at most one additional
// record that is an OPT for the root name, no compression pointer in the
// question, and no bytes after the last record. Every other message
// fails with an error that names the first fault. The question's labels
// are read as they are, so a dot byte inside a label stays a byte for
// the name form rule to refuse.
func ParseQuery(msg []byte) (Query, error) {
	switch {
	case len(msg) < headerLen:
		return Query{}, errShort
	case len(msg) > MaxQuery:
		return Query{}, errLong
	}
	msg = append([]byte(nil), msg...)
	q := Query{ID: u16(msg, 0), Flags: u16(msg, 2)}
	if err := checkHeader(q.Flags, u16(msg, 4), u16(msg, 6), u16(msg, 8), u16(msg, 10)); err != nil {
		return Query{}, err
	}
	labels, off, err := readName(msg, headerLen)
	if err != nil {
		return Query{}, err
	}
	if off+4 > len(msg) {
		return Query{}, errTruncated
	}
	q.Labels, q.Type = labels, u16(msg, off)
	if u16(msg, off+2) != ClassIN {
		return Query{}, errClass
	}
	off += 4
	q.question = msg[headerLen:off]
	if u16(msg, 10) == 1 {
		if off, err = readOPT(msg, off); err != nil {
			return Query{}, err
		}
	}
	if off != len(msg) {
		return Query{}, errTrailing
	}
	return q, nil
}

// checkHeader checks the flags and the record counts.
func checkHeader(flags, qd, an, ns, ar uint16) error {
	switch {
	case flags&flagQR != 0:
		return errResponse
	case flags&opcodeMask != 0:
		return errOpcode
	case qd != 1:
		return errQuestions
	case an != 0 || ns != 0:
		return errRecords
	case ar > 1:
		return errAdditional
	}
	return nil
}

// readName reads the plain labels of a name from off to its root label,
// and returns them and the offset after it.
func readName(msg []byte, off int) ([][]byte, int, error) {
	var labels [][]byte
	for off < len(msg) {
		n := int(msg[off])
		switch {
		case n == 0:
			return labels, off + 1, nil
		case n&labelTypes == labelTypes:
			return nil, 0, errPointer
		case n&labelTypes != 0:
			return nil, 0, errLabelType
		case off+1+n > len(msg):
			return nil, 0, errTruncated
		}
		labels = append(labels, msg[off+1:off+1+n])
		off += 1 + n
	}
	return nil, 0, errTruncated
}

// readOPT reads an OPT record for the root name at off, and returns the
// offset after it. Its options aren't read: the server sends no OPT
// record back.
func readOPT(msg []byte, off int) (int, error) {
	// The root name, type, class, TTL and data length.
	const fixed = 1 + 2 + 2 + 4 + 2
	if off+fixed > len(msg) {
		return 0, errTruncated
	}
	if msg[off] != 0 || u16(msg, off+1) != TypeOPT {
		return 0, errNotOPT
	}
	end := off + fixed + int(u16(msg, off+9))
	if end > len(msg) {
		return 0, errTruncated
	}
	return end, nil
}

func u16(b []byte, off int) uint16 { return binary.BigEndian.Uint16(b[off:]) }

// LogName returns the question's name in the form a log record holds a
// name that hasn't passed the name form: its labels joined with dots,
// through guesttext.LogField. Use it for every name that isn't a
// dnsname.Name.
func (q Query) LogName() string {
	parts := make([]string, len(q.Labels))
	for i, l := range q.Labels {
		parts[i] = string(l)
	}
	return guesttext.LogField(strings.Join(parts, "."), maxLogBytes)
}

// lastLabelIs reports whether the question's last label is want, in any
// case.
func (q Query) lastLabelIs(want string) bool {
	if len(q.Labels) == 0 {
		return false
	}
	return strings.EqualFold(string(q.Labels[len(q.Labels)-1]), want)
}

// The responses. Each starts with the query's ID, the response flags
// and the echoed question. None carries an OPT record. The answer's name
// is a pointer to the question's name at offset 12, so it holds the case
// the guest sent.

// response builds the header and the question.
func (q Query) response(rcode uint16, answers, authority uint16, size int) []byte {
	b := make([]byte, 0, headerLen+len(q.question)+size)
	flags := uint16(flagQR|flagRA) | q.Flags&flagRD | rcode
	b = binary.BigEndian.AppendUint16(b, q.ID)
	b = binary.BigEndian.AppendUint16(b, flags)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint16(b, answers)
	b = binary.BigEndian.AppendUint16(b, authority)
	b = binary.BigEndian.AppendUint16(b, 0)
	return append(b, q.question...)
}

// soaLen is the size of the root SOA record.
const soaLen = 1 + 2 + 2 + 4 + 2 + 1 + 1 + 5*4

// appendSOA appends the SOA record of the root zone, with a TTL and a
// MINIMUM of ttl and every other number 0 (S07-03-dns, "Negative
// answers").
func appendSOA(b []byte, ttl uint32) []byte {
	b = append(b, 0) // the root
	b = binary.BigEndian.AppendUint16(b, TypeSOA)
	b = binary.BigEndian.AppendUint16(b, ClassIN)
	b = binary.BigEndian.AppendUint32(b, ttl)
	b = binary.BigEndian.AppendUint16(b, 1+1+5*4)
	b = append(b, 0, 0)                     // MNAME and RNAME: the root
	b = binary.BigEndian.AppendUint32(b, 0) // SERIAL
	b = binary.BigEndian.AppendUint32(b, 0) // REFRESH
	b = binary.BigEndian.AppendUint32(b, 0) // RETRY
	b = binary.BigEndian.AppendUint32(b, 0) // EXPIRE
	return binary.BigEndian.AppendUint32(b, ttl)
}

// Refusal is the answer to every refused name, and to the local names:
// NXDOMAIN with the root SOA of TTL 0 and MINIMUM 0. It depends on the
// query alone, never on the reason (S07-04-dns-refusals, "Same answer
// for every refusal").
func (q Query) Refusal() []byte {
	return appendSOA(q.response(rcodeNXDomain, 0, 1, soaLen), 0)
}

// answerA is an A answer with a synthetic address.
func (q Query) answerA(addr netip.Addr, ttl uint32) []byte {
	const aLen = 2 + 2 + 2 + 4 + 2 + 4
	b := q.response(rcodeNoError, 1, 0, aLen)
	b = append(b, labelTypes, headerLen) // a pointer to the question's name
	b = binary.BigEndian.AppendUint16(b, TypeA)
	b = binary.BigEndian.AppendUint16(b, ClassIN)
	b = binary.BigEndian.AppendUint32(b, ttl)
	b = binary.BigEndian.AppendUint16(b, 4)
	a := addr.As4()
	return append(b, a[:]...)
}

// emptyAnswer is an empty answer with the root SOA at an A answer's TTL.
func (q Query) emptyAnswer(ttl uint32) []byte {
	return appendSOA(q.response(rcodeNoError, 0, 1, soaLen), ttl)
}

// refused is REFUSED with no records.
func (q Query) refused() []byte { return q.response(rcodeRefused, 0, 0, 0) }

// servFail is SERVFAIL with no records.
func (q Query) servFail() []byte { return q.response(rcodeServFail, 0, 0, 0) }

// TypeName returns a record type's name for log records.
func TypeName(t uint16) string {
	switch t {
	case TypeA:
		return "A"
	case TypeSOA:
		return "SOA"
	case TypeTXT:
		return "TXT"
	case TypeAAAA:
		return "AAAA"
	case TypeSVCB:
		return "SVCB"
	case TypeHTTPS:
		return "HTTPS"
	}
	return "TYPE" + strconv.Itoa(int(t))
}
