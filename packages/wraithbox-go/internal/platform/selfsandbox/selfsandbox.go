// Package selfsandbox confines the calling process with a profile
// compiled into it, and proves the confinement with a deny probe
// (S04-03-self-sandboxing, "Confined first thing in main",
// SEC12-least-privilege).
//
// Each daemon calls Confine first thing in main, with its own profile,
// and exits non-zero when it returns an error. Confine never exits the
// process itself, and never reads a file, the environment or an
// inherited descriptor. Confinement can't be undone and covers every
// thread of the process.
//
// On macOS the profile is a Seatbelt profile, applied with
// sandbox_init_with_parameters without cgo (S12-01-platform-interfaces,
// "Self-sandbox on macOS", X23-sandboxed-daemons). Linux and Windows
// return an error that wraps errors.ErrUnsupported. The package imports
// only the standard library.
package selfsandbox

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Profile is a sandbox profile compiled into the daemon, for example
// with //go:embed. Name is for the log. Text refers to each path it
// needs as (param "NAME") and never holds a path itself
// (X23-sandboxed-daemons, condition 5).
type Profile struct {
	Name string
	Text string
}

// Rule names the rule behind a decision, for the log and the daemon's
// exit message.
type Rule string

// The rules of this package. Each refusal has its own.
const (
	// RuleApplied: the profile applied and the deny probe was denied.
	RuleApplied Rule = "sandbox-applied"
	// RuleProfileInvalid: the profile text is empty or holds a NUL byte,
	// which would cut the C string short.
	RuleProfileInvalid Rule = "sandbox-profile-invalid"
	// RuleProfileParamForm: the text has a (param form other than
	// (param "NAME") with NAME matching [A-Z][A-Z0-9_]*.
	RuleProfileParamForm Rule = "sandbox-profile-param-form"
	// RuleParamMissing: the text names a parameter the call didn't give.
	RuleParamMissing Rule = "sandbox-param-missing"
	// RuleParamUnexpected: the call gave a parameter the text doesn't name.
	RuleParamUnexpected Rule = "sandbox-param-unexpected"
	// RuleParamInvalid: a parameter value is empty or holds a NUL byte.
	RuleParamInvalid Rule = "sandbox-param-invalid"
	// RuleInitFailed: sandbox_init_with_parameters failed, for example on
	// a syntax error or in a process that is already confined.
	RuleInitFailed Rule = "sandbox-init-failed"
	// RuleProbeAllowed: the deny probe succeeded after the call, so the
	// profile doesn't deny what every profile must.
	RuleProbeAllowed Rule = "sandbox-probe-allowed"
	// RuleProbeUnexpected: the deny probe failed with an error other than
	// the one a denial gives.
	RuleProbeUnexpected Rule = "sandbox-probe-unexpected"
)

// Refusal is the error of every refusal in this package. The process is
// unconfined after a refusal with a rule that comes before the call
// (the profile and parameter rules), and confined after the others.
type Refusal struct {
	Rule    Rule
	Profile string

	msg string
	err error
}

func (r *Refusal) Error() string {
	return "selfsandbox: profile " + r.Profile + ": " + string(r.Rule) + ": " + r.msg
}

// Unwrap returns the cause, such as the error of the deny probe.
func (r *Refusal) Unwrap() error { return r.err }

// paramStart finds each place the text calls param, with any space
// after the parenthesis. paramForm is the one form accepted there.
var (
	paramStart = regexp.MustCompile(`\(\s*param\b`)
	paramForm  = regexp.MustCompile(`^\(param "([A-Z][A-Z0-9_]*)"\)`)
)

// checkProfile checks the profile text and the parameters before the
// call, so that a bad set leaves the process unconfined and the daemon
// exits (X23-sandboxed-daemons, conditions 5 and 7). The parameter names
// in the text must equal the keys of params, and each value must be a
// non-empty C string.
func checkProfile(p Profile, params map[string]string) *Refusal {
	refuse := func(rule Rule, format string, args ...any) *Refusal {
		return &Refusal{Rule: rule, Profile: p.Name, msg: fmt.Sprintf(format, args...)}
	}
	if p.Text == "" || strings.ContainsRune(p.Text, 0) {
		return refuse(RuleProfileInvalid, "the profile text is empty or holds a NUL byte")
	}
	named := map[string]bool{}
	for _, loc := range paramStart.FindAllStringIndex(p.Text, -1) {
		m := paramForm.FindStringSubmatch(p.Text[loc[0]:])
		if m == nil {
			end := min(loc[0]+40, len(p.Text))
			return refuse(RuleProfileParamForm, "at byte %d, %q is not (param \"NAME\") with NAME in [A-Z][A-Z0-9_]*", loc[0], p.Text[loc[0]:end])
		}
		named[m[1]] = true
	}
	for _, name := range slices.Sorted(maps.Keys(named)) {
		if _, ok := params[name]; !ok {
			return refuse(RuleParamMissing, "the profile names parameter %s, which the call doesn't give", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(params)) {
		if !named[name] {
			return refuse(RuleParamUnexpected, "the call gives parameter %q, which the profile doesn't name", name)
		}
		if v := params[name]; v == "" || strings.ContainsRune(v, 0) {
			return refuse(RuleParamInvalid, "parameter %s is empty or holds a NUL byte", name)
		}
	}
	return nil
}
