package procgroup

import (
	"os"
	"os/exec"
)

// Windows doesn't start a group, or a job object, yet
// (S06-13-data-disk-format, "When `wb-diskfmt` doesn't exit").
const groups = false

// newGroup leaves cmd as it is.
func newGroup(*exec.Cmd) {}

// watch returns a function that returns at once. kill needs no gate:
// os.Process.Kill terminates through the process handle, which keeps
// the process id from being reused while it is open.
func watch(int) (func(), error) { return func() {}, nil }

// kill stops p alone.
func kill(p *os.Process, _ bool) error { return p.Kill() }
