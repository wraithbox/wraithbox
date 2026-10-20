package procgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// helperEnv makes the test binary a helper program: "sleep" sleeps,
// "exit" exits with status 3, and "parent" starts a sleeping child,
// with no SysProcAttr, as wb-diskfmt starts diskutil, writes the
// child's pid to its standard output and sleeps.
const helperEnv = "PROCGROUP_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "sleep":
		time.Sleep(2 * time.Minute)
	case "exit":
		os.Exit(3)
	case "parent":
		child := helper("sleep")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(2 * time.Minute)
	}
	os.Exit(0)
}

func helper(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:noctx // the tests end it with Kill
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return cmd
}

// TestKill: Kill stops a running program, and once it has exited, sends
// nothing and returns os.ErrProcessDone.
func TestKill(t *testing.T) {
	p, err := Start(helper("sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the program didn't exit after Kill")
	}
	if _, ok := errors.AsType[*exec.ExitError](p.Err()); !ok {
		t.Errorf("Err = %v, want an exit error", p.Err())
	}
	if err := p.Kill(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Kill after the exit = %v, want os.ErrProcessDone", err)
	}
}

// TestExited: a program that exits by itself is reaped, its status
// kept, and Kill sends nothing after it.
func TestExited(t *testing.T) {
	p, err := Start(helper("exit"))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := errors.AsType[*exec.ExitError](p.Err())
	if !ok || e.ExitCode() != 3 {
		t.Errorf("Err = %v, want exit status 3", p.Err())
	}
	if err := p.Kill(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Kill after the exit = %v, want os.ErrProcessDone", err)
	}
}

// TestStartFails: a program that can't start returns the error, and no
// Process.
func TestStartFails(t *testing.T) {
	cmd := exec.Command(os.Args[0] + "-missing") //nolint:noctx // never starts
	if p, err := Start(cmd); err == nil || p != nil {
		t.Errorf("Start = %v, %v", p, err)
	}
}
