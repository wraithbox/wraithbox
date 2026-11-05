package httppolicy

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// String names a source: a built-in part as `builtin
// <profile>/<kind>/<part>`, and a policy rule by its source and rule
// name. Every part of it is printable ASCII: policy.Check refuses any
// other rule name, project id or repository.
func (s Source) String() string {
	switch {
	case s.Builtin:
		kind := string(s.Kind)
		if kind == "" {
			kind = "-"
		}
		return "builtin " + string(s.Profile) + "/" + kind + "/" + s.Part
	case s.RuleName == "":
		return ""
	}
	return sourceName(s.Policy) + " rule " + s.RuleName
}

// sourceName names a policy source: its kind, and its project and
// repository when it has them.
func sourceName(s policy.Source) string {
	out := s.Kind.String()
	if s.Project != "" {
		out += " " + s.Project
	}
	if s.Repository != "" {
		out += " " + s.Repository
	}
	return out
}

func offByNames(offBy []policy.Source) string {
	names := make([]string, 0, len(offBy))
	for _, s := range offBy {
		names = append(names, sourceName(s))
	}
	return strings.Join(names, ", ")
}

// logDecision logs one decision: the VM, generation, host, port,
// method, canonical path, rule, source and mode, never a header value
// or a body (SEC10-audit). A refusal and an allowed violation are
// warnings.
func (e *Engine) logDecision(d Decision) {
	attrs := []slog.Attr{
		slog.String("vm", e.vm),
		slog.Uint64("generation", d.Generation),
		slog.String("host", d.Host.String()),
		slog.Int("port", int(d.Port)),
		slog.String("method", d.Method),
		slog.String("path", d.Path),
		slog.String("rule", d.Rule),
		slog.String("source", d.Source.String()),
		slog.String("mode", d.Mode.String()),
	}
	if d.Broke != "" {
		attrs = append(attrs, slog.String("broke", d.Broke))
	}
	if len(d.OffBy) > 0 {
		attrs = append(attrs, slog.String("off_by", offByNames(d.OffBy)))
	}
	if d.Detail != "" {
		attrs = append(attrs, slog.String("detail", d.Detail))
	}
	level, msg := slog.LevelInfo, "http allowed"
	switch {
	case !d.Allow:
		level, msg = slog.LevelWarn, "http refused"
	case d.Rule == RuleAuditAllowed:
		level, msg = slog.LevelWarn, "http violation allowed by audit"
	}
	e.log.LogAttrs(context.Background(), level, msg, attrs...)
}

// RefusalStatus is the status of a refused request.
const RefusalStatus = http.StatusForbidden

// RefusalBody is the body of the 403 that answers a refused request.
// It names the rule, the host, the method and the canonical path, which
// holds only printable ASCII, and the part and policy for
// `profile-part-off` (NFR06-explained-refusals).
func (d Decision) RefusalBody() string {
	var b strings.Builder
	b.WriteString("Wraith Box refused this request.\n")
	line := func(k, v string) {
		if v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	line("rule", d.Rule)
	line("host", d.Host.String()+":"+strconv.Itoa(int(d.Port)))
	line("method", d.Method)
	line("path", d.Path)
	if d.Rule == RulePartOff {
		line("part", d.Source.String())
		line("turned off by", offByNames(d.OffBy))
	} else {
		line("source", d.Source.String())
	}
	line("detail", d.Detail)
	return b.String()
}
