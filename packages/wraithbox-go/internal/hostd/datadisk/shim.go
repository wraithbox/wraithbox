package datadisk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/launch"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/logpipe"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/procgroup"
)

// Shim starts one wb-diskfmt for a slot.
type Shim interface {
	Start(ctx context.Context, slot string) (Session, error)
}

// Session is one wb-diskfmt run: the exchange of
// proto/wraithbox/diskfmt/v1. A refusal of wb-diskfmt is a
// *ShimRefusal. Every answer is untrusted, and the manager checks it.
type Session interface {
	// Format sends the size and waits for the volume UUID (steps 1 to
	// 6).
	Format(ctx context.Context, sizeGiB uint32) (*diskfmtv1.FormatResult, error)
	// Rename asks for the rename and waits for its result (step 8).
	Rename(ctx context.Context) (*diskfmtv1.RenameResult, error)
	// Close ends the run and waits for wb-diskfmt to exit.
	Close() error
}

// ShimRefusal is a refusal from wb-diskfmt. Both fields are text the
// shim chose, cut to diskfmt.MaxDetail.
type ShimRefusal struct {
	Rule   string
	Detail string
}

func (s *ShimRefusal) Error() string { return s.Rule + ": " + s.Detail }

// How long wb-hostd waits for each answer, and for wb-diskfmt's exit.
// Each diskutil step in wb-diskfmt has its own deadline, and the
// format's wait covers the longest path through them,
// diskfmt.FormatTimeout, with formatAnswerMargin for the checks and the
// exchange around them.
const (
	formatAnswerMargin = time.Minute
	formatAnswerWait   = diskfmt.FormatTimeout + formatAnswerMargin
	renameAnswerWait   = time.Minute
	exitWait           = 30 * time.Second
)

// ErrKilled is in Close's error when wb-diskfmt didn't exit within its
// wait after the exchange closed, and was killed, with its process group
// where the OS has them (S06-13-data-disk-format, "When `wb-diskfmt`
// doesn't exit"). A device it attached may stay attached.
var ErrKilled = errors.New("datadisk: wb-diskfmt didn't exit in time, and was killed")

// ErrKillFailed is in Close's error when wb-diskfmt didn't exit in time,
// and the kill failed. It may still run, and a device it attached may
// stay attached.
var ErrKillFailed = errors.New("datadisk: wb-diskfmt didn't exit in time, and the kill failed")

// ProcessShim starts wb-diskfmt as a process, with the fixed
// environment of its slot's launcher entry (internal/hostd/launch),
// until wb-launcher starts it (S04-04-vm-processes, "`wb-diskfmt`").
// Descriptor 2 is its log pipe, which goes to Logs, and descriptor 3
// one end of a Unix datagram socketpair for the exchange.
type ProcessShim struct {
	// Program is wb-diskfmt in the installed bundle's Contents/MacOS,
	// never from PATH (S17-01-bundle).
	Program string
	// Logs returns the writer of a process's log lines, which logpipe
	// frames and bounds (S04-03-self-sandboxing, "Daemon logs"). nil
	// drops them.
	Logs func(process string) io.Writer
	// Logger gets the starts and exits. nil takes slog.Default().
	Logger *slog.Logger
	// Version is wb-hostd's build version, version.Build(). wb-diskfmt
	// must have the same (S04-08-records-and-bounds, "Between host
	// processes").
	Version string

	// exitWait replaces the package's exitWait when set, for the tests.
	exitWait time.Duration
}

// Start starts wb-diskfmt for slot.
func (p *ProcessShim) Start(ctx context.Context, slot string) (Session, error) {
	entry, ok := launch.Diskfmt(slot)
	if !ok {
		return nil, fmt.Errorf("datadisk: no wb-diskfmt entry for the slot %q", slot)
	}
	if !filepath.IsAbs(p.Program) || filepath.Base(p.Program) != entry.Program {
		return nil, fmt.Errorf("datadisk: wb-diskfmt's path %q isn't an absolute path to %s", p.Program, entry.Program)
	}
	ours, theirs, err := fdchan.Socketpair(diskfmt.MaxRecord)
	if err != nil {
		return nil, err
	}
	defer func() { _ = theirs.Close() }()
	ch, err := fdchan.New(ours, diskfmt.MaxRecord)
	if err != nil {
		return nil, err
	}
	logR, logW, err := os.Pipe()
	if err != nil {
		_ = ch.Close()
		return nil, err
	}
	defer func() { _ = logW.Close() }()
	// No argument, the entry's environment only, and exactly descriptors
	// 0 to 3: os/exec sets close-on-exec on every other one. It leads its
	// own process group where the OS has them, so a kill on a timeout
	// stops the diskutil it runs too.
	cmd := exec.Command(p.Program) //nolint:noctx,gosec // G204: Program is the installed bundle's path from wb-hostd's own configuration, never a request; the run outlives the request's context, with deadlines on each answer and Close to end it
	cmd.Env = append([]string(nil), entry.Env...)
	cmd.Stderr = logW
	cmd.ExtraFiles = []*os.File{theirs}
	proc, err := procgroup.Start(cmd)
	if err != nil {
		_ = ch.Close()
		_ = logR.Close()
		return nil, fmt.Errorf("datadisk: start %s: %w", p.Program, err)
	}
	s := &processSession{ch: ch, proc: proc, log: p.logger(), exitWait: exitWait}
	if p.exitWait > 0 {
		s.exitWait = p.exitWait
	}
	go s.copyLog(context.WithoutCancel(ctx), logR, p.sink(entry.Name), entry.Name)
	s.log.LogAttrs(ctx, slog.LevelInfo, "wb-diskfmt started", slog.String("entry", entry.Name),
		slog.Int("pid", proc.Pid()), slog.Bool("process_group", procgroup.Groups))
	if err := s.versions(p.Version); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// versions sends wb-hostd's version record, the first message on the
// channel, and checks wb-diskfmt's, which must be the same build. A
// refusal is a *hostversion.Refusal, logged with its rule and both
// versions.
func (s *processSession) versions(own string) error {
	if err := s.ch.Send(hostversion.Record(own), nil); err != nil {
		return s.ended(fmt.Errorf("datadisk: send the version record to wb-diskfmt: %w", err))
	}
	if err := s.ch.SetReadDeadline(time.Now().Add(hostversion.Timeout)); err != nil {
		return err
	}
	m, err := s.ch.Recv()
	if err != nil {
		return s.ended(fmt.Errorf("datadisk: no version record from wb-diskfmt: %w", err))
	}
	if m.File != nil {
		_ = m.Close()
		return errors.New("datadisk: wb-diskfmt's version record carried a descriptor")
	}
	_, err = hostversion.CheckRecord(s.log, own, m.Record)
	return err
}

func (p *ProcessShim) logger() *slog.Logger {
	if p.Logger == nil {
		return slog.Default()
	}
	return p.Logger
}

func (p *ProcessShim) sink(process string) io.Writer {
	if p.Logs == nil {
		return io.Discard
	}
	return p.Logs(process)
}

type processSession struct {
	ch       *fdchan.Channel
	proc     *procgroup.Process
	log      *slog.Logger
	exitWait time.Duration
	// refused: wb-diskfmt answered with a refusal, after which it exits 1.
	refused bool
}

func (s *processSession) copyLog(ctx context.Context, r io.ReadCloser, w io.Writer, process string) {
	defer func() { _ = r.Close() }()
	if err := logpipe.Copy(r, w, process); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn, "wb-diskfmt log copy ended",
			slog.String("process", process), slog.String("error", err.Error()))
	}
}

func (s *processSession) Format(_ context.Context, sizeGiB uint32) (*diskfmtv1.FormatResult, error) {
	req := &diskfmtv1.HostMessage{Kind: &diskfmtv1.HostMessage_Format{Format: &diskfmtv1.FormatRequest{SizeGib: sizeGiB}}}
	answer, err := s.ask(req, formatAnswerWait)
	if err != nil {
		return nil, err
	}
	res := answer.GetFormatted()
	if res == nil {
		return nil, errors.New("datadisk: wb-diskfmt's answer to the format isn't a format result")
	}
	return res, nil
}

func (s *processSession) Rename(context.Context) (*diskfmtv1.RenameResult, error) {
	req := &diskfmtv1.HostMessage{Kind: &diskfmtv1.HostMessage_Rename{Rename: &diskfmtv1.RenameRequest{}}}
	answer, err := s.ask(req, renameAnswerWait)
	if err != nil {
		return nil, err
	}
	res := answer.GetRenamed()
	if res == nil {
		return nil, errors.New("datadisk: wb-diskfmt's answer to the rename isn't a rename result")
	}
	return res, nil
}

// ask sends req and reads one answer within wait. A refusal is a
// *ShimRefusal with both texts cut. A message with a descriptor, or one
// that doesn't parse, is an error.
func (s *processSession) ask(req *diskfmtv1.HostMessage, wait time.Duration) (*diskfmtv1.ShimMessage, error) {
	b, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := s.ch.Send(b, nil); err != nil {
		return nil, s.ended(fmt.Errorf("datadisk: send to wb-diskfmt: %w", err))
	}
	if err := s.ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return nil, err
	}
	m, err := s.ch.Recv()
	if err != nil {
		return nil, s.ended(fmt.Errorf("datadisk: no answer from wb-diskfmt: %w", err))
	}
	if m.File != nil {
		_ = m.Close()
		return nil, errors.New("datadisk: wb-diskfmt's answer carried a descriptor")
	}
	var answer diskfmtv1.ShimMessage
	if err := proto.Unmarshal(m.Record, &answer); err != nil {
		return nil, fmt.Errorf("datadisk: wb-diskfmt's answer can't be read: %w", err)
	}
	if r := answer.GetRefusal(); r != nil {
		s.refused = true
		return nil, &ShimRefusal{Rule: bound(r.GetRule()), Detail: bound(r.GetDetail())}
	}
	return &answer, nil
}

// ended adds wb-diskfmt's exit status to err when it has exited.
func (s *processSession) ended(err error) error {
	select {
	case <-s.proc.Done():
		return fmt.Errorf("%w (wb-diskfmt exited: %s)", err, exitText(s.proc.Err()))
	case <-time.After(time.Second):
		return err
	}
}

// exitText says how wb-diskfmt exited.
func exitText(err error) string {
	if err == nil {
		return "status 0"
	}
	return err.Error()
}

// Close closes the exchange, which ends a wb-diskfmt that waits for a
// request, and waits for it to exit. One that doesn't exit in time is
// killed, with its process group, and the error wraps ErrKilled.
func (s *processSession) Close() error {
	_ = s.ch.Close()
	select {
	case <-s.proc.Done():
	case <-time.After(s.exitWait):
		if err := s.kill(); err != nil {
			return err
		}
	}
	waitErr := s.exited()
	// Exit status 1 follows a refusal, which the answer already carried.
	// Any other end of the process, such as a crash after the rename, is
	// returned, and the caller logs it.
	if _, ok := errors.AsType[*exec.ExitError](waitErr); ok && s.refused {
		return nil
	}
	return waitErr
}

// exited waits for wb-diskfmt's exit, logs it, and returns its wait
// error.
func (s *processSession) exited() error {
	err := s.proc.Err()
	s.log.LogAttrs(context.Background(), slog.LevelInfo, "wb-diskfmt exited",
		slog.Int("pid", s.proc.Pid()), slog.Int("status", s.proc.ExitCode()))
	return err
}

// kill kills wb-diskfmt, with its process group where the OS has them,
// so a diskutil it started stops too, and waits up to exitWait for the
// exit. It logs the decision and its rule. It returns nil when
// wb-diskfmt exited on its own first, an error that wraps ErrKilled
// after the kill, and one that wraps ErrKillFailed when the kill
// failed.
func (s *processSession) kill() error {
	ctx := context.Background()
	err := s.proc.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	attrs := []slog.Attr{
		slog.String("rule", string(RuleShimExit)), slog.String("decision", "kill"),
		slog.Int("pid", s.proc.Pid()), slog.Bool("process_group", procgroup.Groups),
		slog.Duration("waited", s.exitWait),
	}
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "wb-diskfmt kill failed", append(attrs, slog.String("error", err.Error()))...)
		return fmt.Errorf("%w: %w", ErrKillFailed, err)
	}
	s.log.LogAttrs(ctx, slog.LevelWarn, "wb-diskfmt killed", attrs...)
	select {
	case <-s.proc.Done():
		return fmt.Errorf("%w (%s)", ErrKilled, exitText(s.exited()))
	case <-time.After(s.exitWait):
		return fmt.Errorf("%w, and it didn't exit within %s of the kill", ErrKilled, s.exitWait)
	}
}
