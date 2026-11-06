package vmcmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/confirm"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
)

// RuleCAListInvalid: wb-hostd listed a CA whose fingerprint isn't 64
// lowercase hexadecimal characters, so wb doesn't show or confirm it.
const RuleCAListInvalid = "vm-ca-list-invalid"

// resetCATimeout bounds each call of the reset: the list, and the drop
// with its records. The confirmation in between has no bound.
const resetCATimeout = time.Minute

// resetCA is `wb vm reset-ca <vm>` (S05-03-management-commands, "`wb vm
// reset-ca`"; S09-06-ca-keys, "Recovery"): it prints the VM's
// inspection CAs with their fingerprints, asks in wb's own terminal, and
// then asks wb-hostd to drop the CAs it showed. No flag is taken: none
// skips the confirmation (SEC14-no-fake-approvals). wb-hostd refuses
// with ca-record-changed when the record changed in between.
func resetCA(ctx context.Context, env Env, args []string) int {
	slot, vm, ok := parseVM(args)
	if !ok {
		return usage(env, "wb vm reset-ca takes one VM, work or isolated, and no flag")
	}
	hostd, code := connect(ctx, env)
	if hostd == nil {
		return code
	}
	defer func() { _ = hostd.Close() }()
	listCtx, cancel := context.WithTimeout(ctx, resetCATimeout)
	defer cancel()
	listed, err := hostd.ListCAs(listCtx, vm)
	if err != nil {
		rule, msg := failureOf(err)
		return fail(env, rule, msg)
	}
	if len(listed) == 0 {
		fmt.Fprintf(env.Stdout, "The %s VM's record has no inspection CA, so nothing changed.\n", slot)
		return exitOK
	}
	prompt, fingerprints, err := resetCAPrompt(slot, listed)
	if err != nil {
		return fail(env, RuleCAListInvalid, err.Error())
	}
	if err := confirm.Ask(env.OpenTerminal, prompt); err != nil {
		if r, ok := errors.AsType[*confirm.Refusal](err); ok {
			return fail(env, string(r.Rule), r.Message)
		}
		return fail(env, string(confirm.RuleDeclined), err.Error())
	}
	resetCtx, cancelReset := context.WithTimeout(ctx, resetCATimeout)
	defer cancelReset()
	dropped, err := hostd.ResetCA(resetCtx, vm, fingerprints)
	if mayHaveRun(err) {
		return fail(env, RuleResetUnknown, fmt.Sprintf(
			"the answer from wb-hostd didn't arrive (%s), and the reset may have run: run wb vm reset-ca %s again to see the %s VM's CAs", err, slot, slot))
	}
	if err != nil {
		rule, msg := failureOf(err)
		return fail(env, rule, msg)
	}
	for _, sha := range dropped {
		fmt.Fprintf(env.Stdout, "Dropped CA SHA-256 %s.\n", printable(sha))
	}
	fmt.Fprintf(env.Stdout, "The %s VM's next start cold boots with a new inspection CA.\n", slot)
	return exitOK
}

// resetCAPrompt is the confirmation, with each CA's fingerprint and
// dates, and the fingerprints to send back. It refuses a fingerprint
// that isn't a SHA-256 in lowercase hexadecimal, so the text the person
// confirms is the text wb sends.
func resetCAPrompt(slot string, cas []*hostdv1.InspectionCA) (confirm.Prompt, []string, error) {
	lines := []string{fmt.Sprintf("This drops the %s VM's inspection CAs from wb-hostd's record:", slot)}
	fingerprints := make([]string, 0, len(cas))
	for _, ca := range cas {
		sha := ca.GetSha256()
		if !isSHA256Hex(sha) {
			return confirm.Prompt{}, nil, fmt.Errorf("wb-hostd listed a CA fingerprint that isn't SHA-256, %q, so nothing changed", printable(sha))
		}
		fingerprints = append(fingerprints, sha)
		line := fmt.Sprintf("  SHA-256 %s, valid %s to %s", sha, dateOf(ca.GetNotBeforeUnix()), dateOf(ca.GetNotAfterUnix()))
		if ca.GetSigning() {
			line += ", signing"
		}
		lines = append(lines, line)
	}
	lines = append(lines,
		"wb-hostd puts their keys on the removal list and deletes the VM's saved state.",
		"The next start cold boots with a new CA, and the guest's trust is installed again. The VM must be stopped.")
	return confirm.Prompt{Command: "wb vm reset-ca", Lines: lines}, fingerprints, nil
}

// isSHA256Hex reports whether s is 64 lowercase hexadecimal characters.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// dateOf formats a Unix time as a UTC date and time.
func dateOf(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
}
