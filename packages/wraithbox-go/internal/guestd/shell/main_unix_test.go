//go:build darwin || linux

package shell

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

// The test binary runs as the session helper, and as the program in
// place of zsh. This entry point is the seam of decision 4 of #303: the
// production build runs /bin/zsh -l only.
const (
	testHelperArg  = "session-helper-test"
	testProgramArg = "shell-test-program"
	// testRawHelperArg runs a helper that writes the reports of its mode,
	// and then waits to be killed.
	testRawHelperArg = "session-helper-raw"
)

// rawHelper writes the reports of mode on descriptor 3.
func rawHelper(mode string) int {
	start := Report{Kind: ReportStarted, Value: uint32(os.Getpid())}
	rs := map[string][]Report{
		"unknown-kind": {start, {Kind: 9}},
		"after-exit":   {start, {Kind: ReportExited}, {Kind: ReportStopped}},
		"code-256":     {start, {Kind: ReportExited, Value: 256}},
		"exit-lingers": {start, {Kind: ReportExited, Value: 7}},
	}[mode]
	f := os.NewFile(reportFD, "reports")
	for _, r := range rs {
		rec := r.Encode()
		if _, err := f.Write(rec[:]); err != nil {
			return 1
		}
	}
	time.Sleep(time.Hour)
	return 0
}

// TestMain runs the test executable as a helper or a test program when
// a test starts it so. Those exit with syscall.Exit, which skips the
// exit hooks: under go test -cover, os.Exit would write a coverage
// warning to the standard error the tests compare.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 {
		switch os.Args[1] {
		case testHelperArg:
			exe, err := os.Executable()
			if err != nil {
				syscall.Exit(1)
			}
			syscall.Exit(runHelper(helperConfig{
				path: exe, args: []string{exe, testProgramArg, os.Args[2]}, tempDir: sessionproc.UserTempDir,
			}))
		case testProgramArg:
			syscall.Exit(testProgram(os.Args[2]))
		case testRawHelperArg:
			syscall.Exit(rawHelper(os.Args[2]))
		}
	}
	os.Exit(m.Run())
}

// testProgram is the program of a test shell. mode names what it does.
func testProgram(mode string) int {
	if code, ok := strings.CutPrefix(mode, "exit:"); ok {
		fmt.Println("out before exit")
		n, _ := strconv.Atoi(code)
		return n
	}
	switch mode {
	case "info":
		return programInfo()
	case "signals":
		return programSignals()
	case "stop":
		fmt.Println("stopping")
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		fmt.Println("continued")
		return 0
	case "kill-self":
		fmt.Println("bye")
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	case "ignore-hup":
		signal.Ignore(syscall.SIGHUP)
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "sleep":
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "setsid-child":
		return programSetsidChild()
	case "cat":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		fmt.Fprintln(os.Stderr, "stdin eof")
		return 0
	case "epipe":
		return programEPIPE()
	case "stderr":
		fmt.Fprint(os.Stdout, "to-out")
		fmt.Fprint(os.Stderr, "to-err")
		return 0
	case "env":
		return programEnv()
	case "tty-stat":
		var st syscall.Stat_t
		if err := syscall.Fstat(0, &st); err != nil {
			return 3
		}
		fmt.Printf("owner=%d mode=%o\n", st.Uid, st.Mode&0o777)
		return 0
	}
	return 2
}

func programInfo() int {
	fg, _ := sessionproc.ForegroundGroup(os.Stdin)
	sid, _ := sessionproc.Getsid(0)
	pgid, _ := sessionproc.Getpgid(0)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		_ = tty.Close()
	}
	fmt.Printf("pid=%d ppid=%d sid=%d pgid=%d fg=%d ctty=%v end\n", os.Getpid(), os.Getppid(), sid, pgid, fg, err == nil)
	return 0
}

func programSignals() int {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGWINCH)
	fmt.Println("ready")
	for s := range sigs {
		if s == syscall.SIGINT {
			fmt.Println("got SIGINT")
			return 0
		}
		rows, cols, _ := sessionproc.Size(os.Stdin)
		fmt.Printf("got SIGWINCH %dx%d\n", rows, cols)
	}
	return 2
}

func programEPIPE() int {
	signal.Ignore(syscall.SIGPIPE)
	for {
		if _, err := os.Stdout.Write([]byte("line\n")); err != nil {
			if errors.Is(err, syscall.EPIPE) {
				fmt.Fprint(os.Stderr, "EPIPE")
				return 0
			}
			return 3
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func programEnv() int {
	env := os.Environ()
	slices.Sort(env)
	w := bufio.NewWriter(os.Stdout)
	for _, e := range env {
		fmt.Fprintln(w, e)
	}
	wd, _ := os.Getwd()
	fmt.Fprintln(w, "cwd="+wd)
	_ = w.Flush()
	return 0
}

// programSetsidChild starts a child that leaves the POSIX session with
// setsid, prints its PID and exits 0.
func programSetsidChild() int {
	exe, err := os.Executable()
	if err != nil {
		return 3
	}
	p, err := os.StartProcess(exe, []string{exe, testProgramArg, "sleep"}, &os.ProcAttr{
		Files: []*os.File{nil, nil, nil},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return 3
	}
	fmt.Printf("child=%d end\n", p.Pid)
	return 0
}
