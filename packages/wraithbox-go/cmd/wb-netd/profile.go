package main

import "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/selfsandbox"

// profile is wb-netd's compiled-in Seatbelt profile
// (S04-03-self-sandboxing, "`wb-netd`"; SEC12-least-privilege). It has
// no parameters.
//
// It allows nothing beyond what (deny default) leaves: wb-netd works only
// on the descriptors it inherited, which need no rule. It opens no file,
// makes no socket, reaches no service and starts no program, and the
// deny probe's read of /etc/passwd fails (internal/platform/selfsandbox).
// The local time zone is loaded before the call (run), so the log needs
// no file either.
//
// The child tests of this package run a confined wb-netd through a DHCP
// lease, a DNS answer, a stream hand-off and its end with this profile
// (child_darwin_test.go). A rule beyond this list is a change to
// S04-03-self-sandboxing first.
var profile = selfsandbox.Profile{
	Name: "wb-netd",
	Text: `(version 1)
(deny default)
`,
}
