// Package config reads the global settings file, <config>/config.toml
// (S09-01-policy-files, "Files"), with closed keys
// (S17-05-upgrades-and-migrations, "Configuration"). A key this build
// doesn't know refuses the whole file. It is never ignored.
//
// The package only reads. No process writes a file in <config>
// (S17-05-upgrades-and-migrations, "Configuration"). wb-hostd reads the
// file at start, and a refusal puts it in refusing mode
// (S17-05-upgrades-and-migrations, "Refusing mode"). wb is to read it
// as well, for the git key (S08-08-host-git).
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
)

// FileName is the name of the settings file in <config>.
const FileName = "config.toml"

// maxFileSize bounds what is read of the file. A settings file this
// large is a mistake, and the refusal says so.
const maxFileSize = 1 << 20

// The defaults of S06-06-devices-and-warm-start, "Resources": 4 vCPU and
// 8 GB for each VM.
const (
	DefaultCPUCount  = 4
	DefaultMemoryMiB = 8192
)

// Rule names the rule behind a refusal, for the log and for the answer
// wb gets in refusing mode.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleKeyUnknown: the file has a key this build doesn't know, at any
	// level (S17-05-upgrades-and-migrations, "Configuration").
	RuleKeyUnknown Rule = "config-key-unknown"
	// RuleKeyRenamed: the file has a key that a release renamed or
	// removed. The refusal names the new key.
	RuleKeyRenamed Rule = "config-key-renamed"
	// RuleValueInvalid: a known key has a value of the wrong type or out
	// of range.
	RuleValueInvalid Rule = "config-value-invalid"
	// RuleSyntaxInvalid: the file isn't valid TOML.
	RuleSyntaxInvalid Rule = "config-syntax-invalid"
	// RuleFileUnreadable: the file exists but can't be read, isn't a
	// regular file, or is larger than maxFileSize.
	RuleFileUnreadable Rule = "config-file-unreadable"
)

// Config holds the settings. Every field has its default when the file
// doesn't set it.
type Config struct {
	// CPUCount is the number of virtual CPUs of each VM
	// (S06-06-devices-and-warm-start, "Resources"). The host's minimum
	// and maximum are wb-vmd's check, not this package's.
	CPUCount uint32
	// MemoryMiB is the guest memory of each VM in MiB.
	MemoryMiB uint64
	// DataDiskGiB is the nominal size of a VM's new data disk in GiB,
	// read when a disk is made (S06-05-disks, "Disks"). The image is
	// sparse: this is the most it grows to. It is bounded to
	// diskfmt.MinSizeGiB to diskfmt.MaxSizeGiB, the bounds wb-diskfmt
	// checks too.
	DataDiskGiB uint32
	// Git is the git setting: the git binary wb and wb-hostd run, empty
	// for the platform's fixed default (S08-08-host-git, "Which git").
	// It is kept as written. hostgit.Resolve refuses one that isn't an
	// absolute path, and that refusal is a failed git check, not a
	// refused settings file.
	Git string
}

// Defaults returns the settings of a missing file.
func Defaults() Config {
	return Config{CPUCount: DefaultCPUCount, MemoryMiB: DefaultMemoryMiB, DataDiskGiB: diskfmt.DefaultSizeGiB}
}

// file is the closed set of keys. A key is added here only when its
// feature exists. Later packages add theirs: audit_retention_days and
// audit_events_per_minute (S09-11-audit). Pointers tell a missing key
// from a zero.
type file struct {
	CPUCount    *int64  `toml:"vm_cpu_count"`
	MemoryMiB   *int64  `toml:"vm_memory_mib"`
	DataDiskGiB *int64  `toml:"vm_data_disk_gib"`
	Git         *string `toml:"git"`
}

// renamedKeys maps a dotted key that a release renamed or removed to the
// key that replaces it, or to a sentence that says it was removed. It is
// empty in V1. Each entry stays for as long as a user can have the old
// key in a file.
var renamedKeys = map[string]string{}

// Refusal is the error of every refusal in this package. It names the
// file, and the line and key when the rule is about one
// (NFR06-explained-refusals).
type Refusal struct {
	Rule Rule
	File string
	// Line is 1-based, and 0 when the refusal isn't about one line.
	Line int
	// Key is the dotted key the refusal is about, if any.
	Key string
	// NewKey is the key that replaces a renamed one.
	NewKey string

	msg string
	err error
}

func (r *Refusal) Error() string { return r.msg }

// Unwrap returns the cause, such as the decoder's error.
func (r *Refusal) Unwrap() error { return r.err }

// Load reads <dir>/config.toml. A missing file gives Defaults. Any other
// problem gives a *Refusal, and the decision is logged with its rule.
func Load(ctx context.Context, dir string) (Config, error) {
	return load(ctx, filepath.Join(dir, FileName), renamedKeys)
}

func load(ctx context.Context, path string, renamed map[string]string) (Config, error) {
	cfg, r := read(path, renamed)
	if r != nil {
		slog.Default().LogAttrs(ctx, slog.LevelError, "config file refused",
			slog.String("rule", string(r.Rule)), slog.String("decision", "refuse"),
			slog.String("file", r.File), slog.Int("line", r.Line), slog.String("key", r.Key),
			slog.String("reason", r.msg))
		return Config{}, r
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "config file accepted",
		slog.String("file", path), slog.String("decision", "accept"),
		slog.Uint64("vm_cpu_count", uint64(cfg.CPUCount)),
		slog.Uint64("vm_memory_mib", cfg.MemoryMiB),
		slog.Uint64("vm_data_disk_gib", uint64(cfg.DataDiskGiB)),
		slog.String("git", strconv.Quote(cfg.Git)))
	return cfg, nil
}

// read reads, decodes and checks the file. A missing file gives
// Defaults.
func read(path string, renamed map[string]string) (Config, *Refusal) {
	data, r := readFile(path)
	if r != nil {
		return Config{}, r
	}
	if data == nil {
		return Defaults(), nil
	}
	var f file
	if r := decode(path, data, &f, renamed); r != nil {
		return Config{}, r
	}
	return f.check(path)
}

// readFile returns nil, nil for a missing file. It follows a symbolic
// link, because users keep dotfiles in a repository and link them. It
// checks for a regular file before it opens one, because opening a FIFO
// blocks until a writer comes, and again on the open file.
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
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, unreadable(path, "can't be read", err)
	}
	if len(data) > maxFileSize {
		return nil, unreadable(path, fmt.Sprintf("is larger than %d bytes", maxFileSize), nil)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func unreadable(path, what string, err error) *Refusal {
	msg := fmt.Sprintf("the settings file %s %s", path, what)
	if err != nil {
		msg += ": " + err.Error()
	}
	return &Refusal{Rule: RuleFileUnreadable, File: path, msg: msg, err: err}
}

// decode checks every key against target's tags exactly (checkKeys),
// then decodes data into target with unknown keys disallowed, and turns
// the decoder's errors into refusals. It takes the target so a test can
// give it a struct with a nested table.
func decode(path string, data []byte, target any, renamed map[string]string) *Refusal {
	if r := checkKeys(path, data, schemaOf(reflect.TypeOf(target)), renamed); r != nil {
		return r
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(target)
	if err == nil {
		return nil
	}
	if sm, ok := errors.AsType[*toml.StrictMissingError](err); ok {
		return unknownKey(path, sm, renamed)
	}
	de, ok := errors.AsType[*toml.DecodeError](err)
	if !ok {
		return &Refusal{
			Rule: RuleSyntaxInvalid, File: path, err: err,
			msg: fmt.Sprintf("the settings file %s can't be read as TOML: %v", path, err),
		}
	}
	line, _ := de.Position()
	key := strings.Join(de.Key(), ".")
	if key == "" {
		return &Refusal{
			Rule: RuleSyntaxInvalid, File: path, Line: line, err: err,
			msg: fmt.Sprintf("the settings file %s isn't valid TOML at line %d: %v", path, line, de),
		}
	}
	return &Refusal{
		Rule: RuleValueInvalid, File: path, Line: line, Key: key, err: err,
		msg: fmt.Sprintf("the settings file %s has an invalid value for %s at line %d: %v", path, key, line, de),
	}
}

// unknownKey refuses the first unknown key the decoder found. checkKeys
// refuses every unknown key before the decoder runs, so this is a second
// layer: the decoder's strict mode stays on, and its refusal is built as
// checkKeys builds one (refuseKey).
func unknownKey(path string, sm *toml.StrictMissingError, renamed map[string]string) *Refusal {
	first := slices.MinFunc(sm.Errors, func(a, b toml.DecodeError) int {
		la, _ := a.Position()
		lb, _ := b.Position()
		return la - lb
	})
	line, _ := first.Position()
	r := refuseKey(path, line, []string{strings.Join(first.Key(), ".")}, renamed)
	r.err = sm
	return r
}

// check applies the ranges and the defaults. Each count must be a
// positive integer, and must fit the field of the VM provider's
// contract (proto/wraithbox/vmd/v1: uint32 CPUs, uint64 MiB).
func (f file) check(path string) (Config, *Refusal) {
	cfg := Defaults()
	if f.CPUCount != nil {
		v := *f.CPUCount
		if v <= 0 || v > math.MaxUint32 {
			return Config{}, outOfRange(path, "vm_cpu_count", v)
		}
		cfg.CPUCount = uint32(v)
	}
	if f.MemoryMiB != nil {
		v := *f.MemoryMiB
		if v <= 0 {
			return Config{}, outOfRange(path, "vm_memory_mib", v)
		}
		cfg.MemoryMiB = uint64(v)
	}
	if f.DataDiskGiB != nil {
		v := *f.DataDiskGiB
		if v < diskfmt.MinSizeGiB || v > diskfmt.MaxSizeGiB {
			return Config{}, &Refusal{
				Rule: RuleValueInvalid, File: path, Key: "vm_data_disk_gib",
				msg: fmt.Sprintf("the settings file %s sets vm_data_disk_gib to %d: a data disk is %d to %d GiB",
					path, v, diskfmt.MinSizeGiB, diskfmt.MaxSizeGiB),
			}
		}
		cfg.DataDiskGiB = uint32(v)
	}
	if f.Git != nil {
		cfg.Git = *f.Git
	}
	return cfg, nil
}

func outOfRange(path, key string, v int64) *Refusal {
	return &Refusal{
		Rule: RuleValueInvalid, File: path, Key: key,
		msg: fmt.Sprintf("the settings file %s sets %s to %d, which isn't a positive integer in range", path, key, v),
	}
}
