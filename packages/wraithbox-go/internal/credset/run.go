package credset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// Store is the part of the secret store wb-credset calls. The store of
// internal/platform/secretstore satisfies it. wb-credset never fetches
// (S15-03-access-rules, rule 1), so there is no Fetch here.
type Store interface {
	Write(ctx context.Context, item storeitem.Name, role storeitem.Role, value []byte) error
	Delete(ctx context.Context, item storeitem.Name, role storeitem.Role) error
	List(ctx context.Context, role storeitem.Role) ([]storeitem.Name, error)
}

// storeError is the error of a refused or failed store call, which
// names its rule and the backend's status. The *Error of
// internal/platform/secretstore has these methods.
type storeError interface {
	error
	StoreRule() storeitem.Rule
	StoreStatus() int32
}

// request is the one operation of a run, after the checks.
type request struct {
	op      string
	binding string
	item    storeitem.Name
}

// Process is what wb-credset runs with: Args are its arguments without
// the program name, and Environ its environment as name=value.
type Process struct {
	Args    []string
	Environ []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Run is wb-credset after the store is open and the process is confined
// (S15-02-secret-store, "`wb-credset`"). It checks p's arguments and
// environment, then makes one store call: write the value on stdin,
// delete the item, or print the binding names to stdout. It logs one
// decision line on stderr, with the rule, the operation, the binding and
// the store's status, and never the value. It returns 0 after the
// operation, and 1 after a refusal or a failed store call.
func Run(ctx context.Context, p Process, store Store) int {
	log := slog.New(slog.NewJSONHandler(p.Stderr, nil))
	req, reason := check(p.Args, p.Environ)
	if reason != "" {
		return refuse(ctx, log, request{}, reason)
	}
	switch req.op {
	case OpWrite:
		return write(ctx, log, req, p.Stdin, store)
	case OpDelete:
		return storeResult(ctx, log, req, store.Delete(ctx, req.item, storeitem.RoleBinding))
	default:
		return list(ctx, log, req, p.Stdout, store)
	}
}

// check runs the checks of S15-02-secret-store, "`wb-credset`", before any
// store call, and returns the request, or the reason of the
// credset-refused refusal. It enforces SEC12-least-privilege for
// wb-credset, and SEC04-no-guest-secrets: the value only comes on
// standard input, so an argument or a variable that could carry one is
// refused. A refusal names a bad operation, variable or binding name by
// its length only, never its text, which could be a value pasted in the
// wrong place.
func check(args, environ []string) (request, string) {
	if len(args) != 2 {
		return request{}, fmt.Sprintf("wb-credset takes exactly two arguments, the operation and the binding name, and got %d", len(args))
	}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(environment, name) {
			return request{}, fmt.Sprintf("the environment has a variable (an entry of %d bytes) that isn't on wb-credset's fixed list", len(kv))
		}
	}
	op, arg := args[0], args[1]
	switch op {
	case OpList:
		if arg != ListArg {
			return request{}, "list takes " + ListArg + " as its second argument, and no binding name"
		}
		return request{op: op}, ""
	case OpWrite, OpDelete:
	default:
		return request{}, fmt.Sprintf("the operation (%d bytes) isn't write, delete or list", len(op))
	}
	item, ok := BindingItem(arg)
	if !ok {
		return request{}, fmt.Sprintf("the binding name (%d bytes) isn't 1 to %d characters of a-z, 0-9 and -", len(arg), storeitem.MaxPart)
	}
	return request{op: op, binding: arg, item: item}, ""
}

// refuse logs a credset-refused decision and returns 1.
func refuse(ctx context.Context, log *slog.Logger, req request, reason string) int {
	logDecision(ctx, log, Decision{Decision: DecisionRefuse, Rule: RuleRefused, Op: req.op, Binding: req.binding, Reason: reason})
	return 1
}

// write reads the value from stdin to its end, writes it, and zeroes
// its buffer (S15-03-access-rules, rules 3 and 5). An empty value or one
// over MaxValue is refused before the store call.
func write(ctx context.Context, log *slog.Logger, req request, stdin io.Reader, store Store) int {
	buf := make([]byte, MaxValue+1)
	defer clear(buf)
	n, err := io.ReadFull(stdin, buf)
	switch {
	case err == nil:
		return refuse(ctx, log, req, fmt.Sprintf("the value is longer than %d bytes", MaxValue))
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
	default:
		return refuse(ctx, log, req, "reading the value from standard input failed: "+err.Error())
	}
	if n == 0 {
		return refuse(ctx, log, req, "the value on standard input is empty")
	}
	return storeResult(ctx, log, req, store.Write(ctx, req.item, storeitem.RoleBinding, buf[:n]))
}

// list prints the binding names, sorted, one per line. It never fetches
// a value (S15-03-access-rules, rule 1).
func list(ctx context.Context, log *slog.Logger, req request, stdout io.Writer, store Store) int {
	items, err := store.List(ctx, storeitem.RoleBinding)
	if err != nil {
		return storeResult(ctx, log, req, err)
	}
	names := make([]string, 0, len(items))
	for _, n := range items {
		names = append(names, bindingOf(n))
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		logDecision(ctx, log, Decision{Decision: DecisionRefuse, Rule: RuleOutputFailed, Op: req.op, Reason: "writing the names failed: " + err.Error()})
		return 1
	}
	logDecision(ctx, log, Decision{Decision: DecisionAccept, Rule: RuleDone, Op: req.op, Reason: fmt.Sprintf("%d bindings", len(names))})
	return 0
}

// storeResult logs the decision of a store call's result and returns
// the exit status (S15-02-secret-store, "Failed store calls").
//
//   - A delete of an item that is already gone is a success, so
//     `wb cred rm` can run twice.
//   - A locked store is store-locked. wb-credset doesn't wait for the
//     unlock: it has no work to resume, and the user runs the command
//     again (S15-02-secret-store, "`wb-credset`").
//   - Any other failure is the store's rule. An error that names none,
//     as on a platform without a backend, is store-write-failed for a
//     write or a delete and store-read-failed for a list.
func storeResult(ctx context.Context, log *slog.Logger, req request, err error) int {
	d := Decision{Op: req.op, Binding: req.binding}
	if err == nil {
		d.Decision, d.Rule = DecisionAccept, RuleDone
		logDecision(ctx, log, d)
		return 0
	}
	d.Decision, d.Reason = DecisionRefuse, err.Error()
	rule := storeitem.RuleWriteFailed
	if req.op == OpList {
		rule = storeitem.RuleReadFailed
	}
	if se, ok := errors.AsType[storeError](err); ok {
		rule, d.Status = se.StoreRule(), se.StoreStatus()
	}
	d.Rule = string(rule)
	switch {
	case rule == storeitem.RuleNotFound && req.op == OpDelete:
		d.Decision, d.Reason = DecisionAccept, "the item was already gone"
		logDecision(ctx, log, d)
		return 0
	case rule == storeitem.RuleLocked:
		d.Reason = "the store is locked. wb-credset doesn't wait for the unlock: unlock it and run the command again"
	}
	logDecision(ctx, log, d)
	return 1
}
