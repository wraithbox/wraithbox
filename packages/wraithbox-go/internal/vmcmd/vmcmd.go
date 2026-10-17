// Package vmcmd is `wb vm` (S05-03-management-commands, "`wb vm`"). It
// has `wb vm reset-data <vm>`, which gives a stopped VM a new data disk
// after a person confirms in wb's own terminal
// (S05-03-management-commands, "`wb vm reset-data`";
// SEC14-no-fake-approvals). `start`, `stop`, `suspend` and `status` come
// with I286.
package vmcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/confirm"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The exit statuses (S05-02-exit-status-and-signals, "Exit status").
const (
	exitOK      = 0
	exitUsage   = 2
	exitFailure = 255
)

// The rules of wb's own refusals in this package.
const (
	// RuleUnavailable: wb can't find its paths.
	RuleUnavailable = "vm-paths-unavailable"
	// RuleUnreachable: wb-hostd can't be reached, or answered without a
	// rule.
	RuleUnreachable = "hostd-unreachable"
	// RuleResetUnknown: the connection ended or timed out after the
	// request went out. wb-hostd finishes a reset it began, so it may
	// have run.
	RuleResetUnknown = "vm-reset-unknown"
)

// resetTimeout bounds the reset: wb-diskfmt's format and the records.
const resetTimeout = 20 * time.Minute

// Hostd is the part of wb-hostd's contract this command calls.
type Hostd interface {
	ResetVMData(ctx context.Context, vm hostdv1.VM) (*hostdv1.ResetVMDataResponse, error)
	Close() error
}

// Env is what the command runs with. SystemEnv builds wb's.
type Env struct {
	Stdout, Stderr io.Writer
	// OpenTerminal opens wb's own terminal for the confirmation
	// (internal/platform/tty).
	OpenTerminal func() (io.ReadWriteCloser, error)
	// DataDir is <data>, for the data disk's path and usage.
	DataDir string
	// Allocated returns the bytes a file takes on the host, from its
	// st_blocks (internal/platform/diskimage).
	Allocated func(path string) (int64, error)
	// Dial connects to wb-hostd.
	Dial func(ctx context.Context) (Hostd, error)
	// Unavailable is set when the paths can't be found.
	Unavailable error
	Log         *slog.Logger
}

// Run runs `wb vm <args>` and returns the exit status.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		return usage(env, "wb vm needs a subcommand: reset-data")
	}
	switch args[0] {
	case "reset-data":
		return resetData(ctx, env, args[1:])
	case "start", "stop", "suspend", "status":
		fmt.Fprintf(env.Stderr, "wb vm %s: not implemented yet\n", args[0])
		return exitUsage
	}
	return usage(env, fmt.Sprintf("wb vm has no subcommand %q", printable(args[0])))
}

func usage(env Env, msg string) int {
	fmt.Fprintf(env.Stderr, "wb: %s\n\nUsage: wb vm reset-data <work|isolated>\n", msg)
	return exitUsage
}

// fail prints the last line of a failure, `wraith box: <message>
// (<rule>)`, and returns 255.
func fail(env Env, rule, msg string) int {
	fmt.Fprintf(env.Stderr, "wraith box: %s (%s)\n", printable(msg), printable(rule))
	return exitFailure
}

// parseVM reads the VM argument. No flag is taken: none skips the
// confirmation (SEC14-no-fake-approvals).
func parseVM(args []string) (string, hostdv1.VM, bool) {
	if len(args) != 1 {
		return "", hostdv1.VM_VM_UNSPECIFIED, false
	}
	switch args[0] {
	case vmbundle.SlotWork:
		return args[0], hostdv1.VM_VM_WORK, true
	case vmbundle.SlotIsolated:
		return args[0], hostdv1.VM_VM_ISOLATED, true
	}
	return "", hostdv1.VM_VM_UNSPECIFIED, false
}

// resetData is `wb vm reset-data <vm>` (S05-03-management-commands,
// "`wb vm reset-data`"): it prints the VM, the data disk's usage on the
// host and what is lost, asks in wb's own terminal, and then asks
// wb-hostd for the reset.
func resetData(ctx context.Context, env Env, args []string) int {
	slot, vm, ok := parseVM(args)
	if !ok {
		return usage(env, "wb vm reset-data takes one VM, work or isolated, and no flag")
	}
	if env.Unavailable != nil {
		return fail(env, RuleUnavailable, "wb can't find its data directory: "+env.Unavailable.Error())
	}
	disk := filepath.Join(vmbundle.Path(env.DataDir, slot), vmbundle.DataDiskName)
	prompt := confirm.Prompt{Command: "wb vm reset-data", Lines: []string{
		fmt.Sprintf("This deletes the %s VM's data disk, %s, %s.", slot, disk, usageOf(env, disk)),
		"Every project's home on that disk is lost: its Claude Code state, and commits not yet pushed.",
		"wb-hostd then makes a new, empty data disk. The VM must be stopped.",
	}}
	if err := confirm.Ask(env.OpenTerminal, prompt); err != nil {
		if r, ok := errors.AsType[*confirm.Refusal](err); ok {
			return fail(env, string(r.Rule), r.Message)
		}
		return fail(env, string(confirm.RuleDeclined), err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	hostd, err := env.Dial(ctx)
	if err != nil {
		return fail(env, RuleUnreachable, "wb-hostd can't be reached, so nothing changed: "+err.Error())
	}
	defer func() { _ = hostd.Close() }()
	res, err := hostd.ResetVMData(ctx, vm)
	if mayHaveRun(err) {
		return fail(env, RuleResetUnknown, fmt.Sprintf(
			"the answer from wb-hostd didn't arrive (%s), and the reset may have run: check the %s VM with wb vm status before you start it", err, slot))
	}
	if err != nil {
		rule, msg := failureOf(err)
		return fail(env, rule, msg)
	}
	if old := res.GetOldVolumeUuid(); old != "" {
		fmt.Fprintf(env.Stdout, "The %s VM's data disk, volume %s, is deleted.\n", slot, printable(old))
	}
	fmt.Fprintf(env.Stdout, "The %s VM has a new data disk, volume %s.\n", slot, printable(res.GetNewVolumeUuid()))
	return exitOK
}

// usageOf says what the disk takes on the host, from its st_blocks.
func usageOf(env Env, disk string) string {
	n, err := env.Allocated(disk)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "which doesn't exist yet"
	case err != nil:
		return "whose size on the host can't be read: " + err.Error()
	}
	return "which takes " + bytesText(n) + " on the host"
}

// bytesText formats n bytes in GiB with one decimal, or MiB below 1 GiB.
func bytesText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// mayHaveRun reports whether err leaves open whether wb-hostd ran the
// request: a deadline, or a connection that ended after the request
// went out. wb-hostd runs a reset to its end without its caller.
func mayHaveRun(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	st, ok := status.FromError(err)
	if !ok || err == nil {
		return false
	}
	code := st.Code()
	return code == codes.DeadlineExceeded || code == codes.Canceled || code == codes.Unavailable
}

// failureOf reads a refusal from wb-hostd: the ErrorInfo reason is the
// rule, and the status message the text. An error without a reason is
// RuleUnreachable.
func failureOf(err error) (string, string) {
	if r, ok := errors.AsType[*versionError](err); ok {
		return r.rule, r.msg
	}
	st, ok := status.FromError(err)
	if !ok {
		return RuleUnreachable, err.Error()
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() != "" {
			return info.GetReason(), st.Message()
		}
	}
	return RuleUnreachable, "wb-hostd failed: " + st.Message()
}

// versionError is a build version refusal of the connection, with both
// versions in its message (S04-08-records-and-bounds, "Between host
// processes").
type versionError struct {
	rule, msg string
}

func (v *versionError) Error() string { return v.msg }

// maxPrinted bounds a text from wb-hostd that wb prints.
const maxPrinted = 2048

// printable removes control and escape characters from s and cuts it,
// so text that came from another process can't rewrite the terminal
// (SEC14-no-fake-approvals).
func printable(s string) string {
	if len(s) > maxPrinted {
		s = strings.ToValidUTF8(s[:maxPrinted], "") + " [cut]"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return '?'
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
}
