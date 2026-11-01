package eventchan

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	auditv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/audit/v1"
)

// maxNoticeAhead is how far ahead a notice's end may be: the end of a
// UTC day, and an hour for a clock that moved.
const maxNoticeAhead = 25 * time.Hour

// notice is a checked BudgetNotice.
type notice struct {
	vm    VM
	rule  Rule
	until time.Time
}

// noticeRefusal is the reason a notice was refused, for the log.
type noticeRefusal struct{ msg string }

func (r *noticeRefusal) Error() string { return string(RuleNoticeInvalid) + ": " + r.msg }

// checkNotice decodes and checks one BudgetNotice from wb-hostd. own is
// the sender's VM for wb-netd, which takes notices for its own VM only,
// and nil for wb-proxyd, which serves both. A notice whose end is past
// is accepted, and has no effect.
func checkNotice(record []byte, own *VM, now time.Time) (notice, *noticeRefusal) {
	msg := &auditv1.BudgetNotice{}
	if err := proto.Unmarshal(record, msg); err != nil {
		return notice{}, &noticeRefusal{fmt.Sprintf("record of %d bytes doesn't decode: %v", len(record), err)}
	}
	if n := len(msg.ProtoReflect().GetUnknown()); n > 0 {
		return notice{}, &noticeRefusal{fmt.Sprintf("%d bytes of fields BudgetNotice doesn't have", n)}
	}
	vm, ok := vmOf(msg.GetVm())
	if !ok {
		return notice{}, &noticeRefusal{"no VM, or one with no slot or generation"}
	}
	if own != nil && vm != *own {
		return notice{}, &noticeRefusal{fmt.Sprintf("notice for %s, this sender serves %s", vm, *own)}
	}
	var rule Rule
	switch msg.GetRule() {
	case auditv1.BudgetRule_BUDGET_RULE_EVENTS_PER_MINUTE:
		rule = RuleBudget
	case auditv1.BudgetRule_BUDGET_RULE_BYTES_PER_DAY:
		rule = RuleBytes
	case auditv1.BudgetRule_BUDGET_RULE_UNSPECIFIED:
		return notice{}, &noticeRefusal{"no rule"}
	default:
		return notice{}, &noticeRefusal{fmt.Sprintf("unknown rule %d", msg.GetRule())}
	}
	until := time.Unix(msg.GetUntilUnix(), 0)
	if until.After(now.Add(maxNoticeAhead)) {
		return notice{}, &noticeRefusal{fmt.Sprintf("end %d is more than %s ahead", msg.GetUntilUnix(), maxNoticeAhead)}
	}
	return notice{vm: vm, rule: rule, until: until}, nil
}
