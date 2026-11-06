package stream

import (
	"context"
	"log/slog"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/handoff"
)

// logCut is how many bytes of a guest string a log record holds: the
// longest name of S07-03-dns.
const logCut = 253

// streamAttrs are a stream's log attributes: the VM, its generation, the
// stream id, the host and the port (S07-15-non-http-streams, "Logging a
// reset").
func streamAttrs(st *stream) []slog.Attr {
	return []slog.Attr{
		slog.String("vm", st.vm),
		slog.Uint64("generation", st.gen),
		slog.Uint64("stream_id", st.id),
		slog.String("host", guesttext.LogField(st.name.String(), logCut)),
		slog.Int("port", int(st.port)),
	}
}

// The decisions of a refusal's log record: how it ended the stream.
const (
	decisionReset = "reset"
	decisionClose = "close"
)

// reset ends a stream with a reset, so the guest sees a TCP reset
// (S07-15-non-http-streams, "Logging a reset"), and logs it. A stream
// that the hand-off ended first was logged there, and isn't again.
func (h *Handler) reset(st *stream, rule string, check egressmsg.Check, attrs ...slog.Attr) {
	if st.ctl.Reset() {
		h.refused(st, decisionReset, rule, check, attrs...)
	}
}

// refused logs a refusal through ratelog, per rule, with the stream, the
// rule, how it ended the stream, the check and what the first bytes
// looked like, never the bytes (S07-15-non-http-streams, "Logging a
// reset"; SEC10-audit), and gives it to the audit hook.
func (h *Handler) refused(st *stream, decision, rule string, check egressmsg.Check, attrs ...slog.Attr) {
	looks := st.looks
	if looks == "" {
		looks = looksUnknown
	}
	all := make([]slog.Attr, 0, len(attrs)+9)
	all = append(all, slog.String("rule", rule), slog.String("decision", decision))
	if check != "" {
		all = append(all, slog.String("check", string(check)))
	}
	all = append(all, slog.String("looks_like", looks))
	all = append(all, streamAttrs(st)...)
	all = append(all, attrs...)
	h.refusals.LogAttrs(context.Background(), slog.LevelWarn, "egress stream refused", all...)
	if h.audit != nil {
		h.audit.Refused(handoff.Event{VM: st.vm, Generation: st.gen, Rule: rule, Check: check})
	}
}

// allowed logs a stream that goes on, one record per stream, with the
// rule that allowed it (SEC10-audit). Until M3.10 connects the audit
// log, this record is where an allowed stream is seen (I496).
func (h *Handler) allowed(st *stream, rule string, mode Mode, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+9)
	all = append(all, slog.String("rule", rule), slog.String("decision", "allow"), slog.String("mode", mode.String()))
	all = append(all, streamAttrs(st)...)
	all = append(all, attrs...)
	h.log.LogAttrs(context.Background(), slog.LevelInfo, "egress stream allowed", all...)
}

// ended logs the end of a relay that was allowed: how it ended, and the
// bytes it relayed each way.
func (h *Handler) ended(st *stream, how string, toUpstream, toGuest int64) {
	all := append(streamAttrs(st),
		slog.String("end", how), slog.Int64("bytes_up", toUpstream), slog.Int64("bytes_down", toGuest))
	h.log.LogAttrs(context.Background(), slog.LevelInfo, "egress relay ended", all...)
}

// entryAttrs name the policy entry that allowed a pass or raw stream:
// its source and reason, as `wb policy explain` lists them
// (S07-07-hosts-and-modes, "pass"; S07-15-non-http-streams, "Raw TCP
// entries").
func entryAttrs(src policy.Source, reason string) []slog.Attr {
	name := src.Kind.String()
	if src.Project != "" {
		name += " " + src.Project
	}
	return []slog.Attr{
		slog.String("source", name),
		slog.String("reason", guesttext.LogField(strings.TrimSpace(reason), logCut)),
	}
}
