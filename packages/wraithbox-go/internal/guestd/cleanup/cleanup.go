// Package cleanup keeps wb-guestd's cleanup records: the result of the
// last kill of each project user, until wb-hostd acknowledges it
// (S06-10-leftover-processes, "Kill IDs and records";
// S06-11-reconnect-and-recovery, "The list").
//
// The records are files in DirName, a directory of the data volume's
// root that root owns with mode 700, outside every home, so no project
// user can read or change one. A record is guest input all the same:
// guest root can write any file, so each one is read with a size cap,
// never through a link, and through the one parser Parse, which has the
// fuzz target FuzzCleanupRecordFile. A file that fails is logged with
// RuleFile and skipped.
package cleanup

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// MaxLeft is the most processes left a record holds: internal/guestd/
// procs' MaxLeft, which the host shows as "more than 1000".
const MaxLeft = 1001

// MaxSize is the largest record file Parse takes. The longest record
// Format writes is 90 bytes.
const MaxSize = 256

// header is the first line of every record, with the format's version.
const header = "wb-cleanup 1"

// Record is the result of one kill.
type Record struct {
	// KillID is the kill ID wb-hostd assigned, never 0.
	KillID uint64
	// ProjectID is the project's id, 32 lowercase hexadecimal digits.
	ProjectID string
	// Left is the count of processes the kill left, 0 to MaxLeft. 0 is a
	// clean kill.
	Left uint32
}

// Clean reports whether the kill left no process.
func (r Record) Clean() bool { return r.Left == 0 }

// Check reports the first value of r out of its bounds.
func (r Record) Check() error {
	switch {
	case r.KillID == 0:
		return errors.New("kill ID 0")
	case !projectuser.ValidProjectID(r.ProjectID):
		return errors.New("project ID not in the form")
	case r.Left > MaxLeft:
		return fmt.Errorf("%d processes left, over %d", r.Left, MaxLeft)
	}
	return nil
}

// Format is the file of r: four lines, each a key and a canonical
// decimal or the project ID.
//
//	wb-cleanup 1
//	kill <kill ID>
//	project <project ID>
//	left <processes left>
func Format(r Record) ([]byte, error) {
	if err := r.Check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\nkill %d\nproject %s\nleft %d\n", header, r.KillID, r.ProjectID, r.Left)
	return b.Bytes(), nil
}

// Parse reads a file Format wrote, and refuses anything else: another
// header, a line out of order, missing or repeated, a number with a
// sign or a leading zero, a value out of Record's bounds, a line with
// trailing bytes, or more than MaxSize bytes.
func Parse(data []byte) (Record, error) {
	if len(data) > MaxSize {
		return Record{}, fmt.Errorf("%d bytes, over %d", len(data), MaxSize)
	}
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) != 5 || len(lines[4]) != 0 {
		return Record{}, errors.New("not four lines that each end in a newline")
	}
	if string(lines[0]) != header {
		return Record{}, errors.New("not a version 1 record")
	}
	var r Record
	var err error
	if r.KillID, err = number(lines[1], "kill ", 64); err != nil {
		return Record{}, err
	}
	project, ok := bytes.CutPrefix(lines[2], []byte("project "))
	if !ok {
		return Record{}, errors.New("line 3 isn't the project")
	}
	r.ProjectID = string(project)
	left, err := number(lines[3], "left ", 32)
	if err != nil {
		return Record{}, err
	}
	if left > MaxLeft {
		return Record{}, fmt.Errorf("%d processes left, over %d", left, MaxLeft)
	}
	r.Left = uint32(left)
	if err := r.Check(); err != nil {
		return Record{}, err
	}
	return r, nil
}

// number reads line as key followed by a canonical decimal of at most
// bits bits.
func number(line []byte, key string, bits int) (uint64, error) {
	digits, ok := bytes.CutPrefix(line, []byte(key))
	if !ok {
		return 0, fmt.Errorf("no %q line", key)
	}
	n, err := canonical(string(digits), bits)
	if err != nil {
		return 0, fmt.Errorf("%q: %w", key, err)
	}
	return n, nil
}

// canonical reads s as a decimal of at most bits bits, in the one form
// strconv.FormatUint writes: digits only, and no leading zero.
func canonical(s string, bits int) (uint64, error) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, errors.New("not a canonical decimal")
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not a canonical decimal")
		}
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, errors.New("out of range")
	}
	return n, nil
}

// FileName is the name of the record file of uid: its canonical
// decimal.
func FileName(uid uint32) string { return strconv.FormatUint(uint64(uid), 10) }

// UIDOf reads the UID from a record file's name, and refuses a name
// FileName didn't make for a project user's UID.
func UIDOf(name string) (uint32, error) {
	n, err := canonical(name, 32)
	if err != nil {
		return 0, err
	}
	if n < uint64(projectuser.FirstID) || n > uint64(projectuser.LastID) {
		return 0, fmt.Errorf("UID %d outside the project range", n)
	}
	return uint32(n), nil
}
