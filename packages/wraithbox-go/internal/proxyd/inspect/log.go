package inspect

import (
	"context"
	"log/slog"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/stream"
)

// logCut is how many bytes of a guest string a log record holds: the
// longest name of S07-03-dns.
const logCut = 253

// The decisions of a refusal's log record: a reset of the guest's stream,
// or a refused upstream connection, which the HTTP handler answers.
const (
	decisionReset    = "reset"
	decisionUpstream = "refuse-upstream"
)

// refused logs a refusal through ratelog, per rule, with the stream, the
// rule, the decision and the check (S07-15-non-http-streams, "Logging a
// reset"; SEC10-audit), and gives it to the audit hook. It never logs
// the stream's bytes.
func (t *Terminator) refused(s *stream.Inspected, decision, rule string, check egressmsg.Check, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+9)
	all = append(all, slog.String("rule", rule), slog.String("decision", decision))
	if check != "" {
		all = append(all, slog.String("check", string(check)))
	}
	all = append(all,
		slog.String("mode", s.Mode.String()),
		slog.String("vm", s.VM),
		slog.Uint64("generation", s.Generation),
		slog.Uint64("stream_id", s.ID),
		slog.String("host", guesttext.LogField(s.Name.String(), logCut)),
		slog.Int("port", int(s.Port)),
	)
	all = append(all, attrs...)
	t.refusals.LogAttrs(context.Background(), slog.LevelWarn, "egress stream refused", all...)
	if t.audit != nil {
		t.audit.Refused(handoff.Event{VM: s.VM, Generation: s.Generation, Rule: rule, Check: check})
	}
}
