// Package audittest has a fake audit writer for the tests of the
// packages that record: it keeps each record in memory, and refuses
// with the refusal a test sets.
package audittest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
)

// Recorder is a fake audit writer. Its zero value accepts every record.
// Its methods are safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	records []audit.Record
	refusal *audit.Refusal
	// writeRefusal refuses Write alone.
	writeRefusal *audit.Refusal
}

// Refuse makes CanStart, CanOpenSession, Write and Refusal refuse with
// rule until it is called with an empty rule. Record still keeps every
// record, as the writer queues one while it fails.
func (r *Recorder) Refuse(rule, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule == "" {
		r.refusal = nil
		return
	}
	r.refusal = &audit.Refusal{Rule: rule, Message: message, Sessions: true}
}

// RefuseWrites makes Write alone refuse with rule, as a write that
// fails after the checks passed, until it is called with an empty rule.
func (r *Recorder) RefuseWrites(rule, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule == "" {
		r.writeRefusal = nil
		return
	}
	r.writeRefusal = &audit.Refusal{Rule: rule, Message: message}
}

// Record keeps rec.
func (r *Recorder) Record(rec audit.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

// Write keeps rec and returns nil, or returns the set refusal without
// keeping it.
func (r *Recorder) Write(_ context.Context, rec audit.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refusal != nil {
		return r.copyRefusal()
	}
	if r.writeRefusal != nil {
		c := *r.writeRefusal
		return &c
	}
	r.records = append(r.records, rec)
	return nil
}

// CanStart returns the set refusal, or nil.
func (r *Recorder) CanStart(string) error {
	return r.refusalErr()
}

// CanOpenSession returns the set refusal, or nil.
func (r *Recorder) CanOpenSession() error {
	return r.refusalErr()
}

// Refusal returns the set refusal, or nil.
func (r *Recorder) Refusal() *audit.Refusal {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refusal == nil {
		return nil
	}
	return r.copyRefusal()
}

func (r *Recorder) refusalErr() error {
	if ref := r.Refusal(); ref != nil {
		return ref
	}
	return nil
}

func (r *Recorder) copyRefusal() *audit.Refusal {
	c := *r.refusal
	return &c
}

// Records returns the records kept so far, in order.
func (r *Recorder) Records() []audit.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Record(nil), r.records...)
}

// Messages returns the message of each record kept so far, in order.
func (r *Recorder) Messages() []string {
	recs := r.Records()
	out := make([]string, len(recs))
	for i, rec := range recs {
		out[i] = rec.Message()
	}
	return out
}

// AsMap is v as its JSON object, as the audit log holds it. It returns
// nil for a value that isn't an object, nil included.
func AsMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// Find returns the records whose message is msg.
func (r *Recorder) Find(msg string) []audit.Record {
	var out []audit.Record
	for _, rec := range r.Records() {
		if rec.Message() == msg {
			out = append(out, rec)
		}
	}
	return out
}
