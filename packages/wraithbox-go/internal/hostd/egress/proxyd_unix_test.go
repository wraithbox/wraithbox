//go:build unix

package egress

import (
	"context"
	"errors"
	"testing"
	"time"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	policyv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/policy/v1"
)

// The payload tests write to a pipe that takes a write deadline, which
// os.Pipe gives on darwin and linux and not on windows. wb-proxyd's
// control channel is an fdchan channel, which only those have, so
// wb-hostd sends no payload on windows (S12-platforms).

// TestPolicyWaitsForAck: Policy sends the policy with a payload pipe and
// returns once wb-proxyd acknowledged it, and fails the start without
// the ack.
func TestPolicyWaitsForAck(t *testing.T) {
	h := newProxyd(t, 100*time.Millisecond)
	if err := h.p.Start(&egressv1.ProxydStartPayload{}); err != nil {
		t.Fatal(err)
	}
	if m, hasFD := h.conn.nextControl(t); m.GetProxydStart() == nil || !hasFD {
		t.Fatalf("sent %v, want ProxydStart with its payload", m)
	}
	errs := make(chan error, 1)
	go func() { errs <- h.p.Policy(context.Background(), "work", 1, &policyv1.EffectivePolicy{Generation: 1}) }()
	if m, hasFD := h.conn.nextControl(t); m.GetPolicyChange().GetSequence() != 1 || !hasFD {
		t.Fatalf("sent %v, want PolicyChange 1 with its payload", m)
	}
	h.conn.pushMsg(t, &egressv1.ProxydToHostd{Kind: &egressv1.ProxydToHostd_PolicyAck{PolicyAck: &egressv1.PolicyAck{Vm: "work", Sequence: 1}}})
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	err := h.p.Policy(context.Background(), "work", 2, &policyv1.EffectivePolicy{Generation: 1})
	if se, ok := errors.AsType[*SetupError](err); !ok || se.Check != checkAckTimeout {
		t.Fatalf("Policy: %v, want %s", err, checkAckTimeout)
	}
	if got := h.proxyd.got(); len(got) != 0 {
		t.Fatalf("wb-proxyd ended with %q", got)
	}
}

// TestPayloadWriteFails: a payload pipe whose reader closed before the
// write ends wb-proxyd with control-channel-failed (S04-architecture,
// "Control channels").
func TestPayloadWriteFails(t *testing.T) {
	h := newProxyd(t, wait)
	h.pay.closeEarly = true
	if err := h.p.Start(&egressv1.ProxydStartPayload{}); err != nil {
		t.Fatal(err)
	}
	h.proxyd.expect(t, RuleControlFailed)
	expectLog(t, h.logs, `"rule":"control-channel-failed"`, `"check":"payload-write"`)
}
