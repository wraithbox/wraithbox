// Package credset is wb-credset, the helper that `wb cred set`, `wb cred
// rm` and `wb cred list` run for one store operation, and the parts of
// its start interface that wb uses to start it (S15-02-secret-store,
// "`wb-credset`"; S15-03-access-rules, rules 1 and 5).
//
// cmd/wb-credset opens the store and confines itself, then calls Run,
// which holds every check and operation, so each case runs with the fake
// store of secretstore/storetest on every OS. The package doesn't import
// internal/platform/secretstore: wb imports it for the fixed environment
// list, the value cap and the decision log line, and wb may not link the
// store (S15-02-secret-store, "`wb` doesn't hold a group").
package credset

import (
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// The operations, wb-credset's first argument (S15-02-secret-store,
// "`wb-credset`"). They are the access kinds of the declared access table
// (S15-03-access-rules, "Declared access").
const (
	OpWrite  = string(storeitem.AccessWrite)
	OpDelete = string(storeitem.AccessDelete)
	OpList   = string(storeitem.AccessList)
)

// ListArg is the second argument of list, which names no binding.
// wb-credset always takes exactly two arguments (S15-02-secret-store,
// "`wb-credset`").
const ListArg = "-"

// MaxValue is the longest value wb-credset writes, and the longest one
// wb reads for `wb cred set`. A longer value is refused, and nothing is
// written (S15-02-secret-store, "`wb-credset`"; S04-08-records-and-bounds:
// every input has a bound). A token or a key fits well below it.
const MaxValue = 16 << 10

// The rules of wb-credset's own decisions. A failed store call is logged
// with the store's rule instead (storeitem.Rule).
const (
	// RuleRefused: the arguments, the environment, the operation, the
	// name or the value aren't what wb-credset takes, so it makes no
	// store call (S15-02-secret-store, "`wb-credset`").
	RuleRefused = "credset-refused"
	// RuleDone: the operation ran.
	RuleDone = "credset-done"
	// RuleOutputFailed: the list ran, and writing the names to standard
	// output failed.
	RuleOutputFailed = "credset-output-failed"
)

// environment is the fixed list of the variable names wb-credset takes
// (S15-02-secret-store, "`wb-credset`"). It is empty: wb-credset reads
// no variable, and wb starts it with an empty environment. A name added
// here needs a comment that says what needs it.
var environment = []string{}

// Environ returns the environment wb starts wb-credset with: each
// variable of the fixed list that lookup finds, as name=value. It is
// never nil, because a nil environment in os/exec means the parent's.
func Environ(lookup func(string) (string, bool)) []string {
	out := []string{}
	for _, name := range environment {
		if v, ok := lookup(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// bindingPrefix starts the item name of every binding.
const bindingPrefix = storeitem.Prefix + "binding/"

// BindingItem returns the item name of the binding name, the <name> of
// binding/<name> (S15-03-access-rules, "Declared access"), and false when
// the name isn't 1 to 64 characters of a to z, 0 to 9 and -.
func BindingItem(binding string) (storeitem.Name, bool) {
	if !storeitem.ValidPart(binding) {
		return storeitem.Name{}, false
	}
	n, err := storeitem.Parse(bindingPrefix + binding)
	if err != nil || n.Form() != storeitem.FormBinding {
		return storeitem.Name{}, false
	}
	return n, true
}

// bindingOf returns the binding name of a binding item.
func bindingOf(n storeitem.Name) string {
	return strings.TrimPrefix(n.String(), bindingPrefix)
}
