// Package policyfile loads one network policy file
// (S09-01-policy-files): the global `<config>/policy.yaml`, a project's
// `<config>/projects/<id>.policy.yaml`, or a repository's
// `.wraithbox/policy.yaml`. The file is OpenShell v0.1.2's
// `network_policies` schema with Wraith Box's extensions under one
// top-level key, `wraithbox`.
//
// A repository's file is repository data, and the guest can propose a
// change to it (S09-03-project-settings, "Which commit"), so every byte
// is treated as hostile. The loader:
//
//   - refuses a file past the decoder's bounds before it reads a value,
//     and an anchor, alias, merge key, tag or duplicate key;
//   - checks every level against its closed key list;
//   - refuses every rule that S07 and S09 say is refused at load.
//
// So no file loads weaker than it is written. Each refusal names its
// rule, the file, the key path and line, and the value at fault,
// escaped (NFR06-explained-refusals). The checks that need more than
// one source are made over the union (M3.3c).
package policyfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// File is a loaded network policy file. Every entry carries the
// file's source.
type File struct {
	Name           string
	Source         policy.Source
	Endpoints      []policy.Endpoint
	PassHosts      []policy.PassHost
	RawTCP         []policy.RawTCP
	PartsOff       []policy.PartOff
	WildcardBudget *uint32
}

// policy returns the file as a policy of its own, for the model's
// check.
func (f *File) policy() *policy.Policy {
	p := &policy.Policy{
		Endpoints: f.Endpoints, PassHosts: f.PassHosts, RawTCP: f.RawTCP, PartsOff: f.PartsOff,
	}
	if f.WildcardBudget != nil {
		p.WildcardBudget = *f.WildcardBudget
	}
	return p
}

// LoadFile reads and loads the file at path. A missing file is an
// empty policy. A file that isn't a regular file, or has more than
// MaxFileSize bytes, is refused without being parsed.
func LoadFile(ctx context.Context, path string, src policy.Source) (*File, error) {
	data, r := readFile(path)
	if r != nil {
		rs := &Refusals{File: path, List: []*Refusal{r}}
		logRefusals(ctx, rs)
		return nil, rs
	}
	if data == nil {
		return &File{Name: path, Source: src}, nil
	}
	return Load(ctx, path, data, src)
}

// Load loads a file's bytes. name names the file in refusals and the
// log. It lets a caller hand in bytes read from a git commit
// (S09-03-project-settings, "Which commit"). The error is a *Refusals.
func Load(ctx context.Context, name string, data []byte, src policy.Source) (*File, error) {
	f, rs := guard(name, func() (*File, *Refusals) { return load(name, data, src) })
	if rs != nil {
		logRefusals(ctx, rs)
		return nil, rs
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "policy file accepted",
		slog.String("file", name), slog.String("decision", "accept"),
		slog.String("source", src.Kind.String()),
		slog.Int("endpoints", len(f.Endpoints)), slog.Int("pass_hosts", len(f.PassHosts)),
		slog.Int("raw_tcp", len(f.RawTCP)), slog.Int("parts_off", len(f.PartsOff)))
	return f, nil
}

// load runs the phases in order. A refusal of the bounds, the form or
// the keys stops it before any value is read. The values are all read,
// so one load reports the first refusal of each rule.
func load(name string, data []byte, src policy.Source) (*File, *Refusals) {
	c := newCollector(name)
	switch src.Kind {
	case policy.SourceGlobal, policy.SourceProject, policy.SourceRepository:
	case policy.SourceUnset, policy.SourceBuiltin, policy.SourceApproval:
		fallthrough
	default:
		c.add(RuleSource, 0, "", "", "a %s source has no policy file", src.Kind)
		return nil, c.err()
	}
	f := &File{Name: name, Source: src}
	root := parse(c, data)
	if c.failed() {
		return nil, c.err()
	}
	if root == nil {
		return f, nil
	}
	keyChecker{c: c, src: src}.top(root)
	if c.failed() {
		return nil, c.err()
	}
	d := &decoder{c: c, file: f, names: map[string]int{}}
	d.top(root)
	d.crossChecks()
	if !c.failed() {
		d.modelCheck()
	}
	if c.failed() {
		return nil, c.err()
	}
	return f, nil
}

// guard runs fn, and turns a panic in it into a refusal with
// RuleLoaderPanic, so a fault in the decoder on a hostile file refuses
// the file and doesn't stop wb-hostd. It fails closed: no File comes
// back.
func guard(name string, fn func() (*File, *Refusals)) (f *File, rs *Refusals) {
	defer func() {
		if p := recover(); p != nil {
			f = nil
			rs = &Refusals{File: name, List: []*Refusal{{
				Rule: RuleLoaderPanic, File: name,
				msg: "the loader failed on the file: " + escape(fmt.Sprint(p)),
			}}}
		}
	}()
	return fn()
}

func logRefusals(ctx context.Context, rs *Refusals) {
	for _, r := range rs.List {
		slog.Default().LogAttrs(ctx, slog.LevelError, "policy file refused",
			slog.String("rule", r.Rule), slog.String("decision", "refuse"),
			slog.String("file", r.File), slog.Int("line", r.Line), slog.String("key", r.Key),
			slog.String("value", r.Value), slog.String("reason", r.msg))
	}
}

// readFile returns nil, nil for a missing file, like config.readFile.
// It checks for a regular file before it opens one, because opening a
// FIFO blocks until a writer comes, and again on the open file. It
// reads at most MaxFileSize+1 bytes.
func readFile(path string) ([]byte, *Refusal) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(path, "can't be read", err)
	}
	if !info.Mode().IsRegular() {
		return nil, unreadable(path, "isn't a regular file", nil)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, unreadable(path, "can't be opened", err)
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, unreadable(path, "can't be read", err)
	}
	if !info.Mode().IsRegular() {
		return nil, unreadable(path, "isn't a regular file", nil)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, unreadable(path, "can't be read", err)
	}
	if len(data) > MaxFileSize {
		return nil, &Refusal{
			Rule: RuleYAMLBound, File: path,
			msg: fmt.Sprintf("the file has more than %d bytes", MaxFileSize),
		}
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func unreadable(path, what string, err error) *Refusal {
	msg := "the policy file " + what
	if err != nil {
		msg += ": " + err.Error()
	}
	return &Refusal{Rule: RuleFileUnreadable, File: path, msg: msg}
}
