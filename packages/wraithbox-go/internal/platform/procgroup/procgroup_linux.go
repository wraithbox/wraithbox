package procgroup

import (
	"os"
	"os/exec"
)

// Linux doesn't start a process group yet (S06-13-data-disk-format,
// "When `wb-diskfmt` doesn't exit").
const groups = false

// newGroup leaves cmd as it is.
func newGroup(*exec.Cmd) {}

// watch returns a function that returns at once. kill needs no gate:
// os.Process.Kill signals through a pidfd, or, on a kernel without
// pidfds, waits with waitid and WNOWAIT before it reaps, so the signal
// can't reach another process.
func watch(int) (func(), error) { return func() {}, nil }

// kill stops p alone.
func kill(p *os.Process, _ bool) error { return p.Kill() }
