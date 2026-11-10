package credset

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
)

// DecisionMsg is the message of wb-credset's one decision line, the JSON
// log line on its standard error that wb reads for the outcome
// (S15-06-binding-commands, "`wb-credset`").
const DecisionMsg = "wb-credset decision"

// The decisions.
const (
	DecisionAccept = "accept"
	DecisionRefuse = "refuse"
)

// Decision is wb-credset's decision line. It never holds a value.
type Decision struct {
	Decision string `json:"decision"`
	Rule     string `json:"rule"`
	Op       string `json:"op"`
	Binding  string `json:"binding"`
	// Status is the store's status of a failed call, 0 otherwise.
	Status int32  `json:"status"`
	Reason string `json:"reason"`
}

// logDecision writes d as the decision line.
func logDecision(ctx context.Context, log *slog.Logger, d Decision) {
	level := slog.LevelInfo
	if d.Decision != DecisionAccept {
		level = slog.LevelError
	}
	log.LogAttrs(ctx, level, DecisionMsg,
		slog.String("decision", d.Decision),
		slog.String("rule", d.Rule),
		slog.String("op", d.Op),
		slog.String("binding", d.Binding),
		slog.Int("status", int(d.Status)),
		slog.String("reason", d.Reason))
}

// RefuseStart logs the decision line of a refusal before wb-credset
// reads its arguments, such as when it can't confine itself, so wb
// prints rule instead of a run without a decision. The line names no
// operation and no binding. It writes the line to stderr, as Run does,
// and returns 1, wb-credset's exit status.
func RefuseStart(ctx context.Context, stderr io.Writer, rule, reason string) int {
	logDecision(ctx, slog.New(slog.NewJSONHandler(stderr, nil)), Decision{Decision: DecisionRefuse, Rule: rule, Reason: reason})
	return 1
}

// ruleForm is the form of a rule wb accepts from the decision line.
var ruleForm = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ParseDecision returns the last decision line in stderr, wb-credset's
// standard error. A line that isn't JSON, isn't the decision, or has a
// rule outside the rule form is skipped, so the store's and the
// sandbox's own lines don't count. It returns false when no line is the
// decision. The caller bounds stderr, and strips the reason before it
// prints it.
func ParseDecision(stderr []byte) (Decision, bool) {
	var found Decision
	ok := false
	for line := range bytes.Lines(stderr) {
		var d struct {
			Msg string `json:"msg"`
			Decision
		}
		if json.Unmarshal(line, &d) != nil || d.Msg != DecisionMsg || !ruleForm.MatchString(d.Rule) {
			continue
		}
		found, ok = d.Decision, true
	}
	return found, ok
}
