// Package pktfilter (copied unchanged from the X07 spike) is the X07 spike's ref restriction for git push: a
// pkt-line filter that sits between the guest and `git receive-pack`,
// reads the command list, and refuses the whole push when any command
// names a ref outside refs/heads/wb/<session-id>/ (S08-workspace-and-git).
//
// Throwaway code. Not held to the project gates.
package pktfilter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxPkt is the largest pkt-line git allows (65520 bytes including the
// 4-byte length).
const MaxPkt = 65520

// MaxCommands bounds the command list. A session pushes a handful of refs.
const MaxCommands = 64

// Command is one ref update from the push command list.
type Command struct {
	Old, New, Ref string
}

// Request is the parsed head of a receive-pack request: everything up to
// and including the flush that ends the command list.
type Request struct {
	Shallow  []string
	Commands []Command
	Caps     []string
	Raw      []byte // the exact bytes read, to forward to receive-pack
}

// Refusal is a policy refusal, as opposed to a framing error.
type Refusal struct {
	Ref, Reason string
}

func (r *Refusal) Error() string { return fmt.Sprintf("ref %q: %s", r.Ref, r.Reason) }

// ReadPkt reads one pkt-line. It returns flush=true for "0000". Delim
// ("0001") and response-end ("0002") are protocol v2 only and refused.
func ReadPkt(r io.Reader) (payload []byte, flush bool, raw []byte, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, false, nil, fmt.Errorf("pkt header: %w", err)
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 16)
	if err != nil {
		return nil, false, nil, fmt.Errorf("pkt length %q: not hex", hdr[:])
	}
	// ParseUint would accept a leading sign or underscore in some forms,
	// so check every byte is a hex digit, as git does.
	for _, c := range hdr {
		if !isHex(c) {
			return nil, false, nil, fmt.Errorf("pkt length %q: not hex", hdr[:])
		}
	}
	switch {
	case n == 0:
		return nil, true, hdr[:], nil
	case n < 4:
		return nil, false, nil, fmt.Errorf("pkt length %d: special packet not allowed in push", n)
	case n > MaxPkt:
		return nil, false, nil, fmt.Errorf("pkt length %d: over %d", n, MaxPkt)
	}
	buf := make([]byte, n)
	copy(buf, hdr[:])
	if _, err := io.ReadFull(r, buf[4:]); err != nil {
		return nil, false, nil, fmt.Errorf("pkt payload: %w", err)
	}
	return buf[4:], false, buf, nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

var zeroOID40 = strings.Repeat("0", 40)

// ReadRequest reads the command list of a receive-pack request (protocol
// v0/v1) from r and checks every ref against prefix. It returns a
// *Refusal for a policy violation and a plain error for bad framing.
// Either way the push is refused: the caller never forwards anything
// unless err is nil.
func ReadRequest(r io.Reader, prefix string) (*Request, error) {
	if !strings.HasPrefix(prefix, "refs/heads/") || !strings.HasSuffix(prefix, "/") {
		return nil, errors.New("bad prefix configuration")
	}
	req := &Request{}
	var raw bytes.Buffer
	first := true
	for {
		p, flush, rawPkt, err := ReadPkt(r)
		if err != nil {
			return nil, err
		}
		raw.Write(rawPkt)
		if flush {
			break
		}
		line := string(p)
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "shallow ") {
			oid := strings.TrimPrefix(line, "shallow ")
			if !isOID(oid) {
				return nil, fmt.Errorf("bad shallow line")
			}
			req.Shallow = append(req.Shallow, oid)
			continue
		}
		if strings.HasPrefix(line, "push-cert") {
			return nil, errors.New("signed pushes are not accepted")
		}
		if first {
			if i := strings.IndexByte(line, 0); i >= 0 {
				req.Caps = strings.Fields(line[i+1:])
				line = line[:i]
			}
			first = false
		} else if strings.IndexByte(line, 0) >= 0 {
			return nil, errors.New("NUL in a later command")
		}
		parts := strings.Split(line, " ")
		if len(parts) != 3 {
			return nil, fmt.Errorf("command: want 3 fields, got %d", len(parts))
		}
		c := Command{Old: parts[0], New: parts[1], Ref: parts[2]}
		if !isOID(c.Old) || !isOID(c.New) || len(c.Old) != len(c.New) {
			return nil, errors.New("command: bad object id")
		}
		if len(req.Commands) >= MaxCommands {
			return nil, errors.New("too many commands")
		}
		req.Commands = append(req.Commands, c)
	}
	req.Raw = raw.Bytes()
	// Policy runs only on a fully framed command list, so a refusal can
	// name every ref in its report. A framing error above returns no
	// request at all.
	for _, c := range req.Caps {
		if c == "push-options" {
			return req, &Refusal{Reason: "push options are not accepted"}
		}
	}
	for _, c := range req.Commands {
		if err := CheckRef(c, prefix); err != nil {
			return req, err
		}
	}
	return req, nil
}

// CheckRef is the policy: SEC03-no-host-exec and FR03-work-as-branch say
// the guest only writes its own session branches, so the ref must be
// strictly under prefix, with only refname-safe characters, and deletes
// are refused.
func CheckRef(c Command, prefix string) error {
	ref := c.Ref
	if !strings.HasPrefix(ref, prefix) || len(ref) == len(prefix) {
		return &Refusal{Ref: ref, Reason: "outside " + prefix}
	}
	rest := ref[len(prefix):]
	// Stricter than git's check_refname_format: a small safe alphabet,
	// no empty component, no "." at a component start, no ".lock" suffix.
	for _, comp := range strings.Split(rest, "/") {
		if comp == "" || comp[0] == '.' || strings.HasSuffix(comp, ".lock") || strings.HasSuffix(comp, ".") {
			return &Refusal{Ref: ref, Reason: "bad ref component"}
		}
		for i := 0; i < len(comp); i++ {
			ch := comp[i]
			ok := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
				ch == '-' || ch == '_' || ch == '.'
			if !ok {
				return &Refusal{Ref: ref, Reason: "bad ref character"}
			}
		}
		if strings.Contains(comp, "..") {
			return &Refusal{Ref: ref, Reason: "bad ref component"}
		}
	}
	if strings.Trim(c.New, "0") == "" {
		return &Refusal{Ref: ref, Reason: "deletes are not accepted"}
	}
	return nil
}

// Pkt encodes one pkt-line.
func Pkt(s string) []byte {
	return []byte(fmt.Sprintf("%04x%s", len(s)+4, s))
}

// RefusalResponse builds the report-status a git client shows as
// "! [remote rejected] <ref> (<reason>)", wrapped in side-band 1 when
// the client asked for it, with a side-band 2 message first.
func RefusalResponse(req *Request, cmds []Command, reason string) []byte {
	sideband := false
	report := false
	if req != nil {
		for _, c := range req.Caps {
			switch c {
			case "side-band-64k", "side-band":
				sideband = true
			case "report-status", "report-status-v2":
				report = true
			}
		}
	}
	var inner bytes.Buffer
	inner.Write(Pkt("unpack refused by wraith box\n"))
	for _, c := range cmds {
		inner.Write(Pkt("ng " + c.Ref + " " + reason + "\n"))
	}
	inner.WriteString("0000")
	var out bytes.Buffer
	if !report {
		return nil
	}
	if sideband {
		out.Write(Pkt("\x02wb: push refused: " + reason + "\n"))
		out.Write(Pkt("\x01" + inner.String()))
		out.WriteString("0000")
		return out.Bytes()
	}
	return inner.Bytes()
}

// ZeroOID reports whether s is the all-zero object id.
func ZeroOID(s string) bool { return s == zeroOID40 || strings.Trim(s, "0") == "" }
