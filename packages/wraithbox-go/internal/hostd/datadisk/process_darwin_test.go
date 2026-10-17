package datadisk_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
