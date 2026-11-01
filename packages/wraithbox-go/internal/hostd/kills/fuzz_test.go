package kills

import (
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
)

// FuzzKillResult feeds arbitrary bytes through proto.Unmarshal as a
// KillUser answer and as a cleanup record, into the checks of
// S06-10-leftover-processes, "Acceptance". Whatever the bytes, nothing
// accepted names a kill ID that wasn't sent for its project in this VM
// generation, and a result taken as clean is CLEAN with no count.
func FuzzKillResult(f *testing.F) {
	seed := func(m proto.Message) []byte {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	// The kill IDs are random, so a seed can't name a sent one. The
	// tracker below sends the IDs of the seeds instead.
	f.Add(seed(&guestv1.KillUserResponse{KillId: 1, ProjectId: projA, Result: guestv1.KillResult_KILL_RESULT_CLEAN}), uint8(0))
	f.Add(seed(&guestv1.CleanupRecord{KillId: 2, ProjectId: projB, Result: guestv1.KillResult_KILL_RESULT_PROCESSES_LEFT, ProcessesLeft: 1001}), uint8(1))
	f.Add(seed(&guestv1.CleanupRecord{KillId: 1, ProjectId: projB, Result: guestv1.KillResult_KILL_RESULT_CLEAN}), uint8(1))
	f.Add(seed(&guestv1.KillUserResponse{KillId: 1, ProjectId: projA, Result: 9, ProcessesLeft: 3}), uint8(0))
	f.Add([]byte{}, uint8(1))

	f.Fuzz(func(t *testing.T, data []byte, which uint8) {
		tr, sent := fuzzTracker(t)
		take := fuzzAnswer
		if which%2 == 1 {
			take = fuzzRecord
		}
		if out, accepted := take(t, tr, data); accepted {
			checkAccepted(t, tr, sent, out)
		}
	})
}

// fuzzTracker is a tracker that sent kills in the work VM's generation
// 1, and the map of each kill ID it sent to its project: random ones
// from Begin, and IDs 1 and 2 forced in.
func fuzzTracker(t *testing.T) (*Tracker, map[uint64]string) {
	t.Helper()
	tr := New(Config{Clock: newFakeClock(), Logger: slog.New(slog.DiscardHandler), Audit: &audittest.Recorder{}})
	sent := map[uint64]string{}
	for _, p := range []string{projA, projB} {
		sent[begin(t, tr, vm.SlotWork, 1, p)] = p
	}
	g := tr.slots[vm.SlotWork]
	for id, p := range map[uint64]string{1: projA, 2: projB} {
		g.kills[id] = &kill{id: id, projectID: p, deadline: tr.clock.Now().Add(Deadline)}
		sent[id] = p
	}
	return tr, sent
}

// fuzzAnswer takes data as a KillUser answer, as wb-hostd does.
func fuzzAnswer(_ *testing.T, tr *Tracker, data []byte) (guestlink.KillOutcome, bool) {
	resp := &guestv1.KillUserResponse{}
	if proto.Unmarshal(data, resp) != nil {
		return guestlink.KillOutcome{}, false
	}
	out, r := guestlink.CheckKillUser(resp)
	if r != nil {
		return guestlink.KillOutcome{}, false
	}
	return out, tr.Accept(vm.SlotWork, 1, out, SourceReply)
}

// fuzzRecord takes data as the one cleanup record of a reconnect list.
// A record out of bounds is never acknowledged.
func fuzzRecord(t *testing.T, tr *Tracker, data []byte) (guestlink.KillOutcome, bool) {
	rec := &guestv1.CleanupRecord{}
	if proto.Unmarshal(data, rec) != nil {
		return guestlink.KillOutcome{}, false
	}
	answers, err := tr.AnswerRecords(vm.SlotWork, 1, []*guestv1.CleanupRecord{rec}, 1)
	if err != nil {
		t.Fatalf("a list of one record refused: %v", err)
	}
	out, r := guestlink.CheckCleanupRecord(rec)
	accepted := answers[0] == AnswerAcknowledged
	if accepted && r != nil {
		t.Fatalf("a record out of bounds acknowledged: %v", r)
	}
	return out, accepted
}

// checkAccepted fails unless out names a kill sent for its project, has
// a result in its bounds, and refuses the VM when processes were left.
func checkAccepted(t *testing.T, tr *Tracker, sent map[uint64]string, out guestlink.KillOutcome) {
	t.Helper()
	if p, ok := sent[out.KillID]; !ok || p != out.ProjectID {
		t.Fatalf("accepted kill %d of %q, sent %v", out.KillID, out.ProjectID, sent)
	}
	if out.Clean != (out.ProcessesLeft == 0) || out.ProcessesLeft > guestlink.MaxProcessesLeft {
		t.Fatalf("accepted %+v", out)
	}
	if !out.Clean && tr.CheckAdmission(vm.SlotWork, 1) == nil {
		t.Fatalf("processes left %+v didn't refuse the VM", out)
	}
	if strings.ToLower(out.ProjectID) != out.ProjectID {
		t.Fatalf("accepted the project ID %q", out.ProjectID)
	}
}
