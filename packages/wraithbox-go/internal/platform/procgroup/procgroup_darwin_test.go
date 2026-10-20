package procgroup

import (
	"bufio"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestKillGroup: the program leads its own process group, a child it
// starts is in that group, and Kill stops both.
func TestKillGroup(t *testing.T) {
	cmd := helper("parent")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Err() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := unix.Getpgid(p.Pid()); g != p.Pid() {
		t.Errorf("the program's group is %d, want its own pid %d", g, p.Pid())
	}
	if g, _ := unix.Getpgid(child); g != p.Pid() {
		t.Errorf("the child's group is %d, want the program's %d", g, p.Pid())
	}
	if own := unix.Getpgrp(); own == p.Pid() {
		t.Fatal("the program is in the test's own group")
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.Err()
	// The child's parent is gone, so launchd reaps it.
	deadline := time.Now().Add(10 * time.Second)
	for unix.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child %d still runs after Kill", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSignalGroupRefuses: a pid that names no child's group is refused,
// and nothing is sent. The test sends signal 0, which only checks, so a
// broken check can't harm the test's own group or any other process.
func TestSignalGroupRefuses(t *testing.T) {
	for _, pid := range []int{0, 1, -1, -2} {
		if err := signalGroup(pid, 0); err == nil {
			t.Errorf("signalGroup(%d) sent a signal", pid)
		}
	}
}
