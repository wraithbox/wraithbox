package events

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
)

// invalid is why a record was refused, for the log. Every refusal ends
// the sender with RuleRecordInvalid.
type invalid struct{ reason string }

func (e *invalid) Error() string { return RuleRecordInvalid + ": " + e.reason }

func refuse(format string, args ...any) *invalid {
	return &invalid{reason: fmt.Sprintf(format, args...)}
}

// check decodes and checks one record from src. last is the sequence
// number of src's last accepted record, 0 before the first. live
// reports whether a VM is running, for a sender that serves more than
// one (S04-08-records-and-bounds, "Event records").
func check(record []byte, src Source, last uint64, live func(eventchan.VM) bool) (*auditv1.EventRecord, eventchan.VM, *invalid) {
	rec := &auditv1.EventRecord{}
	if err := proto.Unmarshal(record, rec); err != nil {
		return nil, eventchan.VM{}, refuse("record of %d bytes doesn't decode: %v", len(record), err)
	}
	if n := len(rec.ProtoReflect().GetUnknown()); n > 0 {
		return nil, eventchan.VM{}, refuse("%d bytes of fields EventRecord doesn't have", n)
	}
	if rec.GetSequence() != last+1 {
		return nil, eventchan.VM{}, refuse("sequence %d after %d", rec.GetSequence(), last)
	}
	vm, bad := checkVM(rec.GetVm(), src, live)
	if bad != nil {
		return nil, vm, bad
	}
	switch k := rec.GetKind().(type) {
	case *auditv1.EventRecord_Event:
		bad = checkEvent(k.Event)
	case *auditv1.EventRecord_Run:
		bad = checkRun(k.Run)
	default:
		bad = refuse("neither an event nor a run")
	}
	if bad != nil {
		return nil, vm, bad
	}
	return rec, vm, nil
}

// checkVM checks the VM a record names: wb-netd names its own VM only,
// and wb-proxyd a VM that runs (S04-08-records-and-bounds, "Event
// records").
func checkVM(v *auditv1.VM, src Source, live func(eventchan.VM) bool) (eventchan.VM, *invalid) {
	vm, ok := eventchan.VMOf(v)
	if !ok {
		return vm, refuse("no VM, or one with no slot or generation")
	}
	if src.VM != nil && vm != *src.VM {
		return vm, refuse("%s names %s, its VM is %s", src.Sender, vm, *src.VM)
	}
	if src.VM == nil && !live(vm) {
		return vm, refuse("%s names %s, which isn't running", src.Sender, vm)
	}
	return vm, nil
}

// checkHead checks what an event and a run share: the class, the
// decision and the rule.
func checkHead(class auditv1.EventClass, decision, rule string) *invalid {
	if !eventchan.KnownClass(class) {
		return refuse("unknown class %d", class)
	}
	if !eventchan.ValidDecision(decision) {
		return refuse("decision of %d bytes isn't 1 to %d of [a-z-]", len(decision), eventchan.MaxDecision)
	}
	if !eventchan.ValidRule(rule) {
		return refuse("rule of %d bytes isn't 1 to %d of [a-z0-9-]", len(rule), eventchan.MaxRule)
	}
	return nil
}

// checkEvent checks an event's fields against their bounds. Protobuf
// has checked that each string is valid UTF-8.
func checkEvent(e *auditv1.Event) *invalid {
	if bad := checkHead(e.GetClass(), e.GetDecision(), e.GetRule()); bad != nil {
		return bad
	}
	l := e.GetGuestLabel()
	for _, f := range []struct {
		name     string
		n, limit int
	}{
		{"destination host", len(e.GetDestinationHost()), eventchan.MaxHost},
		{"HTTP method", len(e.GetHttpMethod()), eventchan.MaxMethod},
		{"HTTP path", len(e.GetHttpPath()), eventchan.MaxPath},
		{"label project", len(l.GetProject()), eventchan.MaxLabel},
		{"label session", len(l.GetSession()), eventchan.MaxLabel},
		{"label user", len(l.GetUser()), eventchan.MaxLabel},
		{"label program", len(l.GetProgram()), eventchan.MaxLabel},
	} {
		if f.n > f.limit {
			return refuse("%s of %d bytes, more than %d", f.name, f.n, f.limit)
		}
	}
	if e.GetDestinationPort() > eventchan.MaxPort {
		return refuse("destination port %d", e.GetDestinationPort())
	}
	if e.GetBytesIn() > eventchan.MaxBytes || e.GetBytesOut() > eventchan.MaxBytes {
		return refuse("bytes in %d or out %d over %d", e.GetBytesIn(), e.GetBytesOut(), uint64(eventchan.MaxBytes))
	}
	return nil
}

// checkRun checks a run's count and window.
func checkRun(r *auditv1.Run) *invalid {
	if bad := checkHead(r.GetClass(), r.GetDecision(), r.GetRule()); bad != nil {
		return bad
	}
	if r.GetCount() < 1 || r.GetCount() > eventchan.MaxRunCount {
		return refuse("run count %d isn't 1 to %d", r.GetCount(), uint64(eventchan.MaxRunCount))
	}
	if r.GetWindowSeconds() < 1 || r.GetWindowSeconds() > eventchan.MaxRunWindow {
		return refuse("run window of %d seconds isn't 1 to %d", r.GetWindowSeconds(), eventchan.MaxRunWindow)
	}
	return nil
}
