package vmcmd

import (
	"context"
	"fmt"
	"strings"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// vmStatus is `wb vm status [<vm>]`: for each VM, its state, its
// generation, its guest link with the guest's version or the refusal
// with both versions, and its last error.
func vmStatus(ctx context.Context, env Env, args []string) int {
	slots := []string{vmbundle.SlotWork, vmbundle.SlotIsolated}
	if len(args) > 0 {
		slot, _, ok := parseVM(args)
		if !ok {
			return usage(env, "wb vm status takes at most one VM, work or isolated")
		}
		slots = []string{slot}
	}
	hostd, code := connect(ctx, env)
	if hostd == nil {
		return code
	}
	defer func() { _ = hostd.Close() }()
	for _, slot := range slots {
		_, v, _ := parseVM([]string{slot})
		callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
		s, err := hostd.GetVMStatus(callCtx, v)
		cancel()
		if err != nil {
			rule, msg := failureOf(err)
			return fail(env, rule, msg)
		}
		printStatus(env, slot, s)
	}
	return exitOK
}

// printStatus writes a VM's state. Every text from wb-hostd is printed
// without control and escape characters.
func printStatus(env Env, slot string, s *hostdv1.VMStatus) {
	fmt.Fprintf(env.Stdout, "%s VM: %s, generation %d\n", slot, enumWord(s.GetPhase().String(), "VM_PHASE_"), s.GetGeneration())
	switch s.GetLinkState() {
	case hostdv1.LinkState_LINK_STATE_DOWN:
	case hostdv1.LinkState_LINK_STATE_LINKED:
		fmt.Fprintf(env.Stdout, "  guest link: linked, wb-guestd version %s\n", guestText(s.GetGuestVersion()))
	case hostdv1.LinkState_LINK_STATE_REFUSED:
		fmt.Fprintf(env.Stdout, "  guest link: refused, %s\n", linkRefusalText(s.GetLinkRefusal()))
	case hostdv1.LinkState_LINK_STATE_UNSPECIFIED, hostdv1.LinkState_LINK_STATE_CONNECTING:
		fmt.Fprintf(env.Stdout, "  guest link: %s\n", enumWord(s.GetLinkState().String(), "LINK_STATE_"))
	}
	if e := s.GetLastError(); e != nil {
		fmt.Fprintf(env.Stdout, "  last error: %s\n", lastErrorText(e))
	}
}

// enumWord is an enum value's name without its prefix, in lowercase.
// A value this build doesn't know prints as its number.
func enumWord(name, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}

// linkRefusalText names the rule, both versions when the refusal is
// about the version, and wb-hostd's text with the fix.
func linkRefusalText(r *hostdv1.LinkRefusal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "rule %s", printable(r.GetRule()))
	if gv := r.GetGuestVersion(); gv != "" {
		fmt.Fprintf(&b, ", wb-guestd version %s and wb-hostd version %s", guestText(gv), guestText(r.GetHostdVersion()))
	}
	if msg := r.GetMessage(); msg != "" {
		b.WriteString(": " + printable(msg))
	}
	return b.String()
}

// lastErrorText describes a framework error from wb-vmd, or "" for
// none. The text fields came from wb-vmd.
func lastErrorText(e *hostdv1.VMError) string {
	if e == nil {
		return ""
	}
	text := fmt.Sprintf("%s (code %d", printable(e.GetDescription()), e.GetCode())
	if e.GetErrno() != 0 {
		text += fmt.Sprintf(", errno %d", e.GetErrno())
	}
	text += ")"
	if r := e.GetFailureReason(); r != "" {
		text += " " + printable(r)
	}
	return text
}

// guestText is a version the guest reported, as wb prints it: without
// the escaped set of S16-03-around-the-stream, and cut to
// version.MaxLen bytes. wb-hostd checked and quoted it already, and wb
// still treats it as guest input (SEC11-root-gains-nothing,
// S05-03-management-commands, "`wb approve` and `wb deny`").
func guestText(s string) string {
	s = guesttext.Strip(s)
	if len(s) > version.MaxLen {
		s = strings.ToValidUTF8(s[:version.MaxLen], "")
	}
	return s
}
