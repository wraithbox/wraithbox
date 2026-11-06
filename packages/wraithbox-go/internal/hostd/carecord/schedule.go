package carecord

import (
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
)

// Plan is what the schedule asks of a VM's CA record at a time
// (S09-07-ca-certificates, "Rotation").
type Plan struct {
	// Expired holds the CAs past their end (day 60), oldest first. They
	// leave the record, and their items go on the removal list.
	Expired []state.CA
	// Live holds the other CAs, oldest first. Live[0] is the current CA.
	Live []state.CA
	// IssueSuccessor is set from day 30 of the current CA while it is
	// the only live one.
	IssueSuccessor bool
	// SwitchDue is set from day 58 of the current CA while a successor
	// is live: the successor signs, once its install is recorded.
	SwitchDue bool
}

// Due returns the plan for cas, the record of one VM oldest first, at
// now. Days count from the current CA's issue: day 30 issues the
// successor, day 58 moves signing to it, and day 60 is the current
// CA's end, which its certificate carries. A step that came due while
// the VM was off or the host slept is due all the same.
func Due(cas []state.CA, now time.Time) Plan {
	var p Plan
	for _, ca := range cas {
		if expired(ca, now) {
			p.Expired = append(p.Expired, ca)
		} else {
			p.Live = append(p.Live, ca)
		}
	}
	if len(p.Live) == 0 {
		return p
	}
	age := now.Sub(p.Live[0].Issued)
	p.IssueSuccessor = len(p.Live) == 1 && age >= successorAfter
	p.SwitchDue = len(p.Live) > 1 && age >= switchAfter
	return p
}

// expired: wb-proxyd never signs with a CA from its end on
// (S09-07-ca-certificates, "Rotation", day 60).
func expired(ca state.CA, now time.Time) bool {
	return !now.Before(ca.NotAfter)
}

// signer returns the record's signing CA among live, or nil.
func signer(live []state.CA) *state.CA {
	for i := range live {
		if live[i].Signing {
			return &live[i]
		}
	}
	return nil
}

// scheduled returns the CA the schedule has sign: the successor from
// day 58, and the current CA before.
func (p Plan) scheduled() state.CA {
	if p.SwitchDue {
		return p.Live[1]
	}
	return p.Live[0]
}

func ids(cas []state.CA) []string {
	out := make([]string, 0, len(cas))
	for _, ca := range cas {
		out = append(out, ca.ID)
	}
	return out
}

func certs(cas []state.CA) [][]byte {
	out := make([][]byte, 0, len(cas))
	for _, ca := range cas {
		out = append(out, ca.Certificate)
	}
	return out
}
