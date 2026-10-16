package termfilter

import "slices"

// Rule names one row of S16-02-drops-and-links, "Drop rules". The
// filter counts every drop, rewrite and shortened report under one of
// these names, and no other name exists, so a guest can't make one up.
// `wb-hostd` checks the counts `wb` reports against this list
// (S09-12-terminal-filter, "Form").
type Rule string

// The rules in the order of the table in S16-02-drops-and-links.
const (
	// RuleC1 counts C1 controls in text.
	RuleC1 Rule = "terminal-c1"
	// RuleC0 counts the C0 controls and DEL the allowlist drops, and
	// each BEL over the rate.
	RuleC0 Rule = "terminal-c0"
	// RuleESC counts the ESC sequences the allowlist drops.
	RuleESC Rule = "terminal-esc"
	// RuleCSI counts the CSI sequences the allowlist drops, other than
	// those with the final byte t.
	RuleCSI Rule = "terminal-csi"
	// RuleCSIWindow counts the CSI t sequences the allowlist drops:
	// window moves, title reports, the title stack.
	RuleCSIWindow Rule = "terminal-csi-window"
	// RuleOSCTitle counts OSC 0 to 2.
	RuleOSCTitle Rule = "terminal-osc-title"
	// RuleOSCLink counts OSC 8.
	RuleOSCLink Rule = "terminal-osc-link"
	// RuleOSCClipboard counts OSC 52.
	RuleOSCClipboard Rule = "terminal-osc-clipboard"
	// RuleOSCFile counts OSC 7, 1337 and 5113.
	RuleOSCFile Rule = "terminal-osc-file"
	// RuleOSC counts the other OSC sequences the allowlist drops.
	RuleOSC Rule = "terminal-osc"
	// RuleOSCNotify counts OSC 9, 99 and 777, which the filter turns
	// into a BEL rather than drop.
	RuleOSCNotify Rule = "terminal-osc-notify"
	// RuleOSCStatusText counts the OSC 7501 reports the filter passes
	// without their title, msg or other keys, rather than drop.
	RuleOSCStatusText Rule = "terminal-osc-status-text"
	// RuleDCS counts DCS sequences.
	RuleDCS Rule = "terminal-dcs"
	// RuleAPC counts APC sequences, the kitty graphics protocol among
	// them.
	RuleAPC Rule = "terminal-apc"
	// RulePMOrSOS counts PM and SOS sequences.
	RulePMOrSOS Rule = "terminal-pm-sos"
)

var rules = [...]Rule{
	RuleC1,
	RuleC0,
	RuleESC,
	RuleCSI,
	RuleCSIWindow,
	RuleOSCTitle,
	RuleOSCLink,
	RuleOSCClipboard,
	RuleOSCFile,
	RuleOSC,
	RuleOSCNotify,
	RuleOSCStatusText,
	RuleDCS,
	RuleAPC,
	RulePMOrSOS,
}

// Rules returns every rule, in the order of the table in
// S16-02-drops-and-links.
func Rules() []Rule {
	return slices.Clone(rules[:])
}

// Valid reports whether r is one of the rules Rules returns.
func (r Rule) Valid() bool {
	return slices.Contains(rules[:], r)
}
