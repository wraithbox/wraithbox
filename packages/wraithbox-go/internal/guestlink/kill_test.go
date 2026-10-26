package guestlink

import (
	"testing"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// TestCheckKill: a kill result keeps the bounds of the contract or is
// refused with RuleUsersReplyInvalid, as an answer and as a cleanup
// record (S06-10-leftover-processes, S04-08-records-and-bounds).
func TestCheckKill(t *testing.T) {
	const p = "0123456789abcdef0123456789abcdef"
	clean := guestv1.KillResult_KILL_RESULT_CLEAN
	left := guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT
	cases := map[string]struct {
		id     uint64
		result guestv1.KillResult
		n      uint32
		want   *KillOutcome
	}{
		"clean":                  {7, clean, 0, &KillOutcome{KillID: 7, ProjectID: p, Clean: true}},
		"processes left":         {7, left, 3, &KillOutcome{KillID: 7, ProjectID: p, ProcessesLeft: 3}},
		"processes left cap":     {7, left, MaxProcessesLeft, &KillOutcome{KillID: 7, ProjectID: p, ProcessesLeft: MaxProcessesLeft}},
		"kill ID 0":              {0, clean, 0, nil},
		"clean with a count":     {7, clean, 1, nil},
		"left with no count":     {7, left, 0, nil},
		"left over the cap":      {7, left, MaxProcessesLeft + 1, nil},
		"unspecified":            {7, guestv1.KillResult_KILL_RESULT_UNSPECIFIED, 0, nil},
		"value this build lacks": {7, 9, 0, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			outs := map[string]func() (KillOutcome, *UsersRefusal){
				"answer": func() (KillOutcome, *UsersRefusal) {
					return CheckKillUser(&guestv1.KillUserResponse{KillId: c.id, ProjectId: p, Result: c.result, ProcessesLeft: c.n})
				},
				"record": func() (KillOutcome, *UsersRefusal) {
					return CheckCleanupRecord(&guestv1.CleanupRecord{KillId: c.id, ProjectId: p, Result: c.result, ProcessesLeft: c.n})
				},
			}
			for source, check := range outs {
				out, r := check()
				requireKill(t, source, out, r, c.want)
			}
		})
	}
}

// requireKill fails unless the check of source gave want, or, with a nil
// want, a refusal with RuleUsersReplyInvalid.
func requireKill(t *testing.T, source string, out KillOutcome, r *UsersRefusal, want *KillOutcome) {
	t.Helper()
	if want == nil {
		if r == nil || r.Rule != RuleUsersReplyInvalid {
			t.Fatalf("%s: %+v, %v, want %s", source, out, r, RuleUsersReplyInvalid)
		}
		return
	}
	if r != nil || out != *want {
		t.Fatalf("%s: %+v, %v, want %+v", source, out, r, *want)
	}
}

func TestCountLabel(t *testing.T) {
	for n, want := range map[uint32]string{0: "unknown", 1: "1", 1000: "1000", MaxProcessesLeft: "more than 1000"} {
		if got := CountLabel(n); got != want {
			t.Errorf("CountLabel(%d) = %q, want %q", n, got, want)
		}
	}
}
