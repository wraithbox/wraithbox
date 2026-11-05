// Package dnsname holds the name form rule of S07-03-dns, "Name form"
// and "IP literals", and the canonical form of a name
// (S07-06-name-mapping, "Name reports"): lower case, no trailing dot.
//
// One rule decides which names Wraith Box handles at all. wb-netd checks
// it on the labels of every query as they are on the wire (FromWire),
// before any other check. The policy loader, `wb allow` and wb-proxyd's
// name reports check it on a dotted host (FromHost). Both apply the same
// checks, so a host that loads also resolves, and a name that resolves
// is never refused for its form later. So no policy host names an
// address (SEC05-default-deny), and no control byte or non-ASCII
// character in a name reaches a notification (SEC14-no-fake-approvals).
//
// The package has no dependency on wb-netd. It doesn't log: each caller
// logs a refusal with its rule, through its own rate-limited records.
package dnsname

import (
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// RuleForm is the rule of every refusal in this package
// (S07-03-dns, "Name form"). It is untyped, so a caller's own rule type
// takes it as it is.
const RuleForm = "dns-name-form"

// The limits of S07-03-dns, "Name form".
const (
	// maxLabel is the longest label, in bytes.
	maxLabel = 63
	// maxName is the longest name, in bytes, written with dots between
	// its labels and without a trailing dot.
	maxName = 253
)

// Fault names which check of the rule a name failed.
type Fault string

// The faults, in the order the checks run. The label checks run label
// by label, so the first label at fault is the one reported.
const (
	// FaultNoLabels: the root name, or an empty host.
	FaultNoLabels Fault = "no-labels"
	// FaultLabelEmpty: a label of 0 bytes, such as between two dots.
	FaultLabelEmpty Fault = "label-empty"
	// FaultLabelLong: a label longer than 63 bytes.
	FaultLabelLong Fault = "label-long"
	// FaultLabelByte: a byte other than an ASCII letter, digit or
	// hyphen. A dot byte inside a wire label is one.
	FaultLabelByte Fault = "label-byte"
	// FaultLabelHyphen: a label that starts or ends with a hyphen (I124).
	FaultLabelHyphen Fault = "label-hyphen"
	// FaultNameLong: a name longer than 253 bytes written with dots.
	FaultNameLong Fault = "name-long"
	// FaultIPLiteral: a last label that is all digits, or `0x` followed
	// by hexadecimal digits or nothing, so the name is an IPv4 address
	// some resolvers accept (S07-03-dns, "IP literals").
	FaultIPLiteral Fault = "ip-literal"
)

// Refusal is the error of a name that fails the rule. Its message names
// the label's position and the byte or length at fault, and holds only
// printable ASCII (NFR06-explained-refusals). It doesn't hold the name:
// the caller names the host, escaped, in its own message or log record.
//
// FromWire and FromHost return a *Refusal. Compare it with nil before
// storing it in an error, so a nil one doesn't become a non-nil error.
type Refusal struct {
	// Rule is always RuleForm.
	Rule string
	// Fault is the check that failed.
	Fault Fault
	// Label is the 1-based position of the label at fault, and 0 when
	// the fault is about the whole name.
	Label int

	msg string
}

func (r *Refusal) Error() string { return r.Rule + ": " + r.msg }

// Name is a name that passed the rule, in canonical form: ASCII letters,
// digits, hyphens and dots, lower case, without a trailing dot. The zero
// Name is empty and passed no check. Names compare with ==.
type Name struct {
	s string
}

// String returns the canonical form, such as "api.example.com".
func (n Name) String() string { return n.s }

// FromWire checks a name given as its labels as they are on the wire,
// uncompressed and without the root label, the way wb-netd reads a
// query. A dot byte inside a label is a byte at fault.
func FromWire(labels [][]byte) (Name, *Refusal) {
	if len(labels) == 0 {
		return Name{}, refuse(FaultNoLabels, 0, "the name has no labels: the root name is not a host")
	}
	var c canon
	for i, l := range labels {
		if r := checkLabel(i+1, l); r != nil {
			return Name{}, r
		}
		add(&c, l)
	}
	return finish(&c, labels[len(labels)-1])
}

// FromHost checks a host written with dots, the way the policy loader,
// `wb allow` and wb-proxyd's name reports hold it. One trailing dot is
// removed first. A wildcard host is checked by the caller on the part
// after its leading `*.`. A name report must already be in canonical
// form (S07-06-name-mapping), so wb-proxyd also compares the report
// with the String of the Name it gets.
func FromHost(s string) (Name, *Refusal) {
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return Name{}, refuse(FaultNoLabels, 0, "the name has no labels: the root name is not a host")
	}
	var c canon
	var last string
	for i, rest := 1, s; ; i++ {
		l, after, more := strings.Cut(rest, ".")
		if r := checkLabel(i, l); r != nil {
			return Name{}, r
		}
		add(&c, l)
		if !more {
			last = l
			break
		}
		rest = after
	}
	return finish(&c, last)
}

// canon builds the canonical form of a name label by label in a buffer
// of maxName bytes, so a long name or many labels allocate nothing until
// the name is accepted. It counts the length past the buffer.
type canon struct {
	buf [maxName]byte
	n   int
}

// add puts a label, lowered, after a dot unless it is the first.
func add[L label](c *canon, l L) {
	if c.n > 0 {
		c.put('.')
	}
	for i := range len(l) {
		c.put(lower(l[i]))
	}
}

func (c *canon) put(b byte) {
	if c.n < maxName {
		c.buf[c.n] = b
	}
	c.n++
}

// finish runs the checks on the whole name, and returns it when they
// pass.
func finish[L label](c *canon, last L) (Name, *Refusal) {
	if c.n > maxName {
		return Name{}, refuse(FaultNameLong, 0,
			"the name is "+strconv.Itoa(c.n)+" bytes written with dots, longer than "+strconv.Itoa(maxName))
	}
	if isIPLiteral(last) {
		// The label passed checkLabel, so it is printable ASCII.
		return Name{}, refuse(FaultIPLiteral, 0,
			"the last label \""+string(last)+"\" is a number, so the name is an IPv4 address, not a host")
	}
	return Name{s: string(c.buf[:c.n])}, nil
}

// label is a label as FromWire or FromHost holds it.
type label interface{ ~string | ~[]byte }

// checkLabel checks one label: 1 to 63 bytes of ASCII letters, digits
// and hyphens, with no hyphen at either end. pos is its 1-based position.
func checkLabel[L label](pos int, l L) *Refusal {
	switch {
	case len(l) == 0:
		return refuse(FaultLabelEmpty, pos, "label "+strconv.Itoa(pos)+" is empty")
	case len(l) > maxLabel:
		return refuse(FaultLabelLong, pos,
			"label "+strconv.Itoa(pos)+" is "+strconv.Itoa(len(l))+" bytes, longer than "+strconv.Itoa(maxLabel))
	}
	for i := range len(l) {
		if !isLDH(l[i]) {
			return refuse(FaultLabelByte, pos,
				"label "+strconv.Itoa(pos)+" has the byte "+guesttext.LogField(string(l[i:i+1]), 1)+
					" at offset "+strconv.Itoa(i)+": only ASCII letters, digits and hyphens are allowed")
		}
	}
	switch {
	case l[0] == '-':
		return refuse(FaultLabelHyphen, pos, "label "+strconv.Itoa(pos)+" starts with a hyphen")
	case l[len(l)-1] == '-':
		return refuse(FaultLabelHyphen, pos, "label "+strconv.Itoa(pos)+" ends with a hyphen")
	}
	return nil
}

// isIPLiteral reports whether a label that passed checkLabel is all
// digits, or `0x` or `0X` followed by hexadecimal digits or nothing
// (S07-03-dns, "IP literals").
func isIPLiteral[L label](l L) bool {
	digits := true
	for i := range len(l) {
		digits = digits && isDigit(l[i])
	}
	if digits {
		return true
	}
	if len(l) < 2 || l[0] != '0' || lower(l[1]) != 'x' {
		return false
	}
	for i := 2; i < len(l); i++ {
		if !isHex(l[i]) {
			return false
		}
	}
	return true
}

func refuse(f Fault, pos int, msg string) *Refusal {
	return &Refusal{Rule: RuleForm, Fault: f, Label: pos, msg: msg}
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

func isHex(b byte) bool { return isDigit(b) || ('a' <= lower(b) && lower(b) <= 'f') }

func isLDH(b byte) bool { return isDigit(b) || ('a' <= lower(b) && lower(b) <= 'z') || b == '-' }

// lower lowers ASCII letters only: there is no Unicode handling in this
// package, and an A-label (`xn--`) passes as letters, digits and hyphens.
func lower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
