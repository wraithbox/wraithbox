// Package vmcmd is `wb vm` (S05-03-management-commands, "`wb vm`"):
// `start`, `stop`, `suspend` and `status` of the work VM and the
// isolated VM, and `reset-data`, which gives a stopped VM a new data
// disk after a person confirms in wb's own terminal
// (S05-03-management-commands, "`wb vm reset-data`";
// SEC14-no-fake-approvals). Every command but `suspend` asks wb-hostd
// over its local IPC endpoint (proto/wraithbox/hostd/v1).
package vmcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
)

// Hostd is the part of wb-hostd's contract this command calls.
type Hostd interface {
	StartVM(ctx context.Context, vm hostdv1.VM) (*hostdv1.StartVMResponse, error)
	StopVM(ctx context.Context, vm hostdv1.VM) error
	GetVMStatus(ctx context.Context, vm hostdv1.VM) (*hostdv1.VMStatus, error)
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
	// LinkTimeout is how long `wb vm start` waits for the guest link
	// after the VM runs. 0 takes DefaultLinkTimeout.
	LinkTimeout time.Duration
	Log         *slog.Logger
}

// Run runs `wb vm <args>` and returns the exit status.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		return usage(env, "wb vm needs a subcommand")
	}
	switch args[0] {
	case "start":
		return start(ctx, env, args[1:])
	case "stop":
		return stop(ctx, env, args[1:])
	case "suspend":
		return suspend(env, args[1:])
	case "status":
		return vmStatus(ctx, env, args[1:])
	case "reset-data":
		return resetData(ctx, env, args[1:])
	}
	return usage(env, fmt.Sprintf("wb vm has no subcommand %q", printable(args[0])))
}

const usageText = `Usage: wb vm start <work|isolated>
       wb vm stop <work|isolated>
       wb vm suspend <work|isolated>
       wb vm status [<work|isolated>]
       wb vm reset-data <work|isolated>
`

func usage(env Env, msg string) int {
	fmt.Fprintf(env.Stderr, "wb: %s\n\n%s", msg, usageText)
	return exitUsage
}

// fail prints the last line of a failure, `wraith box: <message>
// (<rule>)`, and returns 255.
func fail(env Env, rule, msg string) int {
	fmt.Fprintf(env.Stderr, "wraith box: %s (%s)\n", printable(msg), printable(rule))
	return exitFailure
}

// parseVM reads the one VM argument, work or isolated. A request names
// its VM with the enum, never with a path.
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

// connect checks the paths and dials wb-hostd. On failure it prints the
// failure and returns nil and the exit status.
func connect(ctx context.Context, env Env) (Hostd, int) {
	if env.Unavailable != nil {
		return nil, fail(env, RuleUnavailable, "wb can't find its paths: "+env.Unavailable.Error())
	}
	hostd, err := env.Dial(ctx)
	if err != nil {
		return nil, fail(env, RuleUnreachable, "wb-hostd can't be reached: "+err.Error())
	}
	return hostd, exitOK
}

// mayHaveRun reports whether err leaves open whether wb-hostd ran the
// request: a deadline, or a connection that ended after the request
// went out. wb-hostd runs a start, a stop and a reset to its end
// without its caller.
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
