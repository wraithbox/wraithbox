package datadisk_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// The process tests start this test binary through a link named
// wb-diskfmt. Run under that name, TestMain plays wb-diskfmt: it checks
// its start interface, and answers the exchange.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "wb-diskfmt" {
		os.Exit(fakeShim())
	}
	os.Exit(m.Run())
}

func fakeShim() int {
	fmt.Fprintln(os.Stderr, `{"msg":"fake wb-diskfmt started"}`)
	ch, err := fdchan.New(os.NewFile(3, "exchange"), diskfmt.MaxRecord)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fd 3:", err)
		return 1
	}
	reply := func(m *diskfmtv1.ShimMessage) {
		b, _ := proto.Marshal(m)
		_ = ch.Send(b, nil)
	}
	_ = ch.Send(hostversion.Record(testVersion), nil)
	if rec := receiveRaw(ch); rec == nil {
		return 1
	} else if _, err := hostversion.CheckRecord(nil, testVersion, rec); err != nil {
		return 1
	}
	if filepath.Base(filepath.Dir(os.Args[0])) == stubbornDir {
		return stubbornShim()
	}
	var problems []string
	if len(os.Args) != 1 {
		problems = append(problems, fmt.Sprintf("arguments %q", os.Args[1:]))
	}
	if env := os.Environ(); !slices.Equal(env, []string{diskfmt.SlotEnv + "=work"}) {
		problems = append(problems, fmt.Sprintf("environment %q", env))
	}
	if !receive(ch).GetFormat().ProtoReflect().IsValid() {
		problems = append(problems, "no format request")
	}
	if len(problems) > 0 {
		reply(&diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Refusal{Refusal: &diskfmtv1.Refusal{Rule: "diskfmt-test-start", Detail: strings.Join(problems, "; ")}}})
		return 1
	}
	reply(&diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Formatted{Formatted: &diskfmtv1.FormatResult{VolumeUuid: uuid1, TemporaryName: "data.wbdisk.tmp"}}})
	if receive(ch).GetRename() == nil {
		return 1
	}
	reply(&diskfmtv1.ShimMessage{Kind: &diskfmtv1.ShimMessage_Renamed{Renamed: &diskfmtv1.RenameResult{FinalName: "data.wbdisk"}}})
	return 0
}

// testVersion is the build version of both ends: a test binary is
// unstamped, which no peer accepts.
const testVersion = "0.0.1-test"

// stubbornDir is the directory of a wb-diskfmt link that plays a shim
// stuck in a diskutil run: stubbornShim.
const stubbornDir = "stubborn"

// stubbornShim starts a child, as a diskutil run, writes its pid to
// child.pid next to the link, and then ignores the closed exchange.
func stubbornShim() int {
	child := exec.Command("/bin/sleep", "120") //nolint:noctx // the test checks that the kill of the group stops it
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "child:", err)
		return 1
	}
	pidFile := filepath.Join(filepath.Dir(os.Args[0]), "child.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		return 1
	}
	time.Sleep(2 * time.Minute)
	return 0
}

// TestProcessShimKilled: a wb-diskfmt that doesn't exit after the
// exchange closes is killed with its process group, so the child it
// started stops too, and Close's error wraps ErrKilled. The kill is
// logged with its rule.
func TestProcessShimKilled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), stubbornDir)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "wb-diskfmt")
	if err := os.Symlink(os.Args[0], program); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	shim := &datadisk.ProcessShim{Program: program, Version: testVersion, Logger: slog.New(slog.NewJSONHandler(logs, nil))}
	datadisk.SetExitWait(shim, 200*time.Millisecond)
	run, err := shim.Start(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	child := readChildPid(t, filepath.Join(dir, "child.pid"))
	if err := run.Close(); !errors.Is(err, datadisk.ErrKilled) {
		t.Fatalf("Close = %v, want ErrKilled", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for unix.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the shim's child %d still runs after Close", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := logs.String(); !strings.Contains(got, `"rule":"`+string(datadisk.RuleShimExit)+`"`) || !strings.Contains(got, `"process_group":true`) {
		t.Errorf("the kill isn't logged with its rule:\n%s", got)
	}
}

// readChildPid waits for the stubborn shim to write its child's pid.
func readChildPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(string(b))
			if err != nil || pid <= 1 {
				t.Fatalf("child.pid holds %q", b)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the shim wrote no child.pid")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func receiveRaw(ch *fdchan.Channel) []byte {
	_ = ch.SetReadDeadline(time.Now().Add(30 * time.Second))
	m, err := ch.Recv()
	if err != nil {
		return nil
	}
	return m.Record
}

func receive(ch *fdchan.Channel) *diskfmtv1.HostMessage {
	var msg diskfmtv1.HostMessage
	_ = proto.Unmarshal(receiveRaw(ch), &msg)
	return &msg
}

// TestProcessShim: wb-diskfmt gets no argument, its entry's environment
// only, its log pipe on descriptor 2 and the exchange on descriptor 3,
// and its log lines reach the sink as records of its entry.
func TestProcessShim(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "wb-diskfmt")
	if err := os.Symlink(os.Args[0], program); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	shim := &datadisk.ProcessShim{Program: program, Version: testVersion, Logs: func(p string) io.Writer {
		if p != "wb-diskfmt-work" {
			t.Errorf("log of %s", p)
		}
		return logs
	}}
	run, err := shim.Start(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Format(t.Context(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetVolumeUuid() != uuid1 {
		t.Errorf("format result %v", res)
	}
	renamed, err := run.Rename(t.Context())
	if err != nil || renamed.GetFinalName() != "data.wbdisk" {
		t.Fatalf("rename: %v, %v", renamed, err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "fake wb-diskfmt started") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := logs.String(); !strings.Contains(got, `"process":"wb-diskfmt-work"`) || !strings.Contains(got, "fake wb-diskfmt started") {
		t.Errorf("log records:\n%s", got)
	}
}

// TestProcessShimVersion: a wb-diskfmt of another build version is
// refused before any request.
func TestProcessShimVersion(t *testing.T) {
	program := filepath.Join(t.TempDir(), "wb-diskfmt")
	if err := os.Symlink(os.Args[0], program); err != nil {
		t.Fatal(err)
	}
	shim := &datadisk.ProcessShim{Program: program, Version: "0.0.2-other"}
	run, err := shim.Start(t.Context(), "work")
	if err == nil {
		_ = run.Close()
		t.Fatal("started")
	}
	if r, ok := errors.AsType[*hostversion.Refusal](err); !ok || r.Rule != "peer-version-mismatch" {
		t.Errorf("err = %v, want peer-version-mismatch", err)
	}
}

// TestProcessShimRefusals: a program at another name, or a slot without
// an entry, isn't started.
func TestProcessShimRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ program, slot string }{
		"relative path": {"wb-diskfmt", "work"},
		"another name":  {"/usr/bin/true", "work"},
		"unknown slot":  {"/x/wb-diskfmt", "build"},
	} {
		shim := &datadisk.ProcessShim{Program: tc.program}
		if run, err := shim.Start(t.Context(), tc.slot); err == nil {
			_ = run.Close()
			t.Errorf("%s: started", name)
		}
	}
}
