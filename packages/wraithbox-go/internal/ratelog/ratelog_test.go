package ratelog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

type records struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *records) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *records) list(t *testing.T) []map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for line := range bytes.Lines(r.buf.Bytes()) {
		rec := map[string]any{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

// One record per rule per window gets through, and the next one carries
// the count held back. Records without a rule all pass.
func TestWindowPerRule(t *testing.T) {
	var out records
	clk := &clock{now: time.Unix(1000, 0)}
	log := ratelog.NewAt(slog.New(slog.NewJSONHandler(&out, nil)), 10*time.Second, clk.Now)
	for range 5 {
		log.Warn("refused", "rule", "a")
		log.Warn("refused", "rule", "b")
		log.Info("accepted")
	}
	clk.now = clk.now.Add(10 * time.Second)
	log.Warn("refused", "rule", "a")

	got := out.list(t)
	var a, b, plain int
	for _, rec := range got {
		switch rec["rule"] {
		case "a":
			a++
		case "b":
			b++
		default:
			plain++
		}
	}
	if a != 2 || b != 1 || plain != 5 {
		t.Fatalf("a=%d b=%d plain=%d, want 2, 1, 5: %v", a, b, plain, got)
	}
	last := got[len(got)-1]
	if last["rule"] != "a" || last[ratelog.HeldBackKey] != float64(4) {
		t.Fatalf("last record %v, want rule a with 4 held back", last)
	}
}

// A rule given with WithAttrs counts as the record's rule, and derived
// loggers share the windows.
func TestWithAttrsShares(t *testing.T) {
	var out records
	clk := &clock{now: time.Unix(1000, 0)}
	log := ratelog.NewAt(slog.New(slog.NewJSONHandler(&out, nil)), time.Minute, clk.Now)
	log.With("rule", "a").Warn("one")
	log.With("vm", "work").WithGroup("g").Warn("two", "rule", "a")
	log.Warn("three", "rule", "a")
	if n := len(out.list(t)); n != 1 {
		t.Fatalf("%d records, want 1", n)
	}
}

// A peer that makes up rule names can't grow the handler's state past
// its bound: past it, every new rule shares one window.
func TestRuleBound(t *testing.T) {
	var out records
	clk := &clock{now: time.Unix(1000, 0)}
	log := ratelog.NewAt(slog.New(slog.NewJSONHandler(&out, nil)), time.Minute, clk.Now)
	for i := range 1000 {
		log.Warn("refused", "rule", "r"+strconv.Itoa(i))
	}
	if n := len(out.list(t)); n != 65 {
		t.Fatalf("%d records, want 64 rules and one shared window", n)
	}
}
