package vmcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/confirm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// RuleResetUnknown: the connection ended or timed out after the reset
// request went out. wb-hostd finishes a reset it began, so it may have
// run.
const RuleResetUnknown = "vm-reset-unknown"

// resetTimeout bounds the reset: wb-diskfmt's format and the records.
const resetTimeout = 20 * time.Minute

// resetData is `wb vm reset-data <vm>` (S05-03-management-commands,
// "`wb vm reset-data`"): it prints the VM, the data disk's usage on the
// host and what is lost, asks in wb's own terminal, and then asks
// wb-hostd for the reset. No flag is taken: none skips the confirmation
// (SEC14-no-fake-approvals).
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
