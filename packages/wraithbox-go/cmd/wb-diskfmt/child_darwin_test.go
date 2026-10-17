package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	diskfmtv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/diskfmt/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// testVersion is the version both ends of a test use.
const testVersion = "0.0.1-test"

// shim is a child running as wb-diskfmt, and wb-hostd's end of its
// exchange.
type shim struct {
	t      *testing.T
	cmd    *exec.Cmd
	ch     *fdchan.Channel
	stderr *bytes.Buffer
	done   chan error
}

// startShim starts the test binary as wb-diskfmt with data as <data>,
// the given environment on top of the test's, and descriptor 3 the
// exchange socket.
func startShim(t *testing.T, data string, vars ...string) *shim {
	t.Helper()
	ours, theirs, err := fdchan.Socketpair(diskfmt.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fdchan.New(ours, diskfmt.MaxRecord)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), childEnv+"=1", childData+"="+data, childVersion+"="+testVersion), vars...)
	cmd.ExtraFiles = []*os.File{theirs}
	s := &shim{t: t, cmd: cmd, ch: ch, stderr: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = theirs.Close()
	go func() { s.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = ch.Close() })
	return s
}

// versions sends wb-hostd's version record, and checks the child's.
func (s *shim) versions(own string) {
	s.t.Helper()
	if err := s.ch.Send(hostversion.Record(own), nil); err != nil {
		s.t.Fatal(err)
	}
	if err := s.ch.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		s.t.Fatal(err)
	}
	m, err := s.ch.Recv()
	if err != nil {
		s.t.Fatalf("no version record: %v\n%s", err, s.wait())
	}
	if _, err := hostversion.CheckRecord(nil, testVersion, m.Record); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shim) send(m *diskfmtv1.HostMessage) {
	s.t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.ch.Send(b, nil); err != nil {
		s.t.Fatalf("send: %v\n%s", err, s.stderr)
	}
}

func (s *shim) recv(wait time.Duration) *diskfmtv1.ShimMessage {
	s.t.Helper()
	if err := s.ch.SetReadDeadline(time.Now().Add(wait)); err != nil {
		s.t.Fatal(err)
	}
	m, err := s.ch.Recv()
	if err != nil {
		s.t.Fatalf("recv: %v\n%s", err, s.wait())
	}
	var msg diskfmtv1.ShimMessage
	if err := proto.Unmarshal(m.Record, &msg); err != nil {
		s.t.Fatal(err)
	}
	return &msg
}

// wait waits for the child to exit, and returns its log.
func (s *shim) wait() string {
	select {
	case <-s.done:
	case <-time.After(time.Minute):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
	return s.stderr.String()
}

func formatRequest(gib uint32) *diskfmtv1.HostMessage {
	return &diskfmtv1.HostMessage{Kind: &diskfmtv1.HostMessage_Format{Format: &diskfmtv1.FormatRequest{SizeGib: gib}}}
}

// linkIsolatedToWork makes the isolated slot's bundle a link to the
// work slot's.
func linkIsolatedToWork(t *testing.T, data string) {
	t.Helper()
	work := vmbundle.Path(data, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(work, vmbundle.Path(data, "isolated")); err != nil {
		t.Fatal(err)
	}
}

// checkStartRefused checks that the child exited 1 with rule logged,
// and never reached sandbox_init.
func checkStartRefused(t *testing.T, s *shim, rule string) {
	t.Helper()
	logs := s.wait()
	if code := s.cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("exit status %d, want 1\n%s", code, logs)
	}
	if !strings.Contains(logs, `"rule":"`+rule+`"`) {
		t.Errorf("the log doesn't name %s:\n%s", rule, logs)
	}
	if strings.Contains(logs, "self-sandbox") {
		t.Errorf("the child reached sandbox_init:\n%s", logs)
	}
}

// TestChildStartRefusals: each start rule is logged, and the child exits
// 1, before it confines itself (C11-host-daemons, "Data disk steps").
func TestChildStartRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
		link bool
		rule string
	}{
		{name: "slot missing", rule: ruleSlotMissing},
		{name: "slot unknown", vars: []string{SlotEnv + "=build"}, rule: ruleSlotUnknown},
		{name: "argument", vars: []string{SlotEnv + "=work", childArgs + "=--bundle /tmp"}, rule: ruleArguments},
		{name: "bundle missing", vars: []string{SlotEnv + "=work"}, rule: ruleBundleMissing},
		{name: "bundle linked to the other slot", vars: []string{SlotEnv + "=isolated"}, link: true, rule: ruleSlotBundleMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			if tc.link {
				linkIsolatedToWork(t, data)
			}
			checkStartRefused(t, startShim(t, data, tc.vars...), tc.rule)
		})
	}
}

// TestChildRefusesSize: the child confines itself with the real profile,
// and refuses a size outside the bounds with diskfmt-size-invalid,
// before it creates any file. Nothing is attached.
func TestChildRefusesSize(t *testing.T) {
	data := t.TempDir()
	bundle := vmbundle.Path(data, "work")
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	// A guest-written file in the same bundle, which the profile keeps
	// the shim from opening (SEC15-no-guest-disk-mount).
	system := filepath.Join(bundle, "sys.wbdisk")
	if err := os.WriteFile(system, []byte("guest-written"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShim(t, data, SlotEnv+"=work", childOutside+"="+system)
	s.versions(testVersion)
	s.send(formatRequest(diskfmt.MinSizeGiB - 1))
	got := s.recv(30 * time.Second).GetRefusal()
	logs := s.wait()
	if got.GetRule() != string(diskfmt.RuleSizeInvalid) {
		t.Fatalf("refusal %v, want %s\n%s", got, diskfmt.RuleSizeInvalid, logs)
	}
	if !strings.Contains(logs, `"rule":"sandbox-applied"`) {
		t.Errorf("the child didn't confine itself:\n%s", logs)
	}
	if !strings.Contains(logs, outsideDenied) {
		t.Errorf("the confined child read the bundle's system disk:\n%s", logs)
	}
	entries, err := os.ReadDir(bundle)
	if err != nil || len(entries) != 1 {
		t.Errorf("the bundle holds %v (%v), want only the system disk", entries, err)
	}
}

// TestChildRefusesMessage: a rename before a format, and a request that
// isn't a message of the contract, are each refused with
// diskfmt-message-invalid.
func TestChildRefusesMessage(t *testing.T) {
	for name, record := range map[string][]byte{
		"rename first": mustMarshal(t, &diskfmtv1.HostMessage{Kind: &diskfmtv1.HostMessage_Rename{Rename: &diskfmtv1.RenameRequest{}}}),
		"garbage":      {0xff, 0xff, 0xff},
		"no kind":      {0x18, 0x01},
	} {
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			if err := os.MkdirAll(vmbundle.Path(data, "work"), 0o700); err != nil {
				t.Fatal(err)
			}
			s := startShim(t, data, SlotEnv+"=work")
			s.versions(testVersion)
			if err := s.ch.Send(record, nil); err != nil {
				t.Fatal(err)
			}
			got := s.recv(30 * time.Second).GetRefusal()
			logs := s.wait()
			if got.GetRule() != ruleMessage {
				t.Fatalf("refusal %v, want %s\n%s", got, ruleMessage, logs)
			}
		})
	}
}

// TestChildRefusesVersion: a wb-hostd of another build version gets no
// answer, and the child exits with the version rule logged before it
// reads a request.
func TestChildRefusesVersion(t *testing.T) {
	data := t.TempDir()
	if err := os.MkdirAll(vmbundle.Path(data, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := startShim(t, data, SlotEnv+"=work")
	if err := s.ch.Send(hostversion.Record("0.0.2-other"), nil); err != nil {
		t.Fatal(err)
	}
	s.send(formatRequest(diskfmt.DefaultSizeGiB))
	logs := s.wait()
	if code := s.cmd.ProcessState.ExitCode(); code != 1 || !strings.Contains(logs, `"rule":"peer-version-mismatch"`) {
		t.Errorf("exit %d, log:\n%s", code, logs)
	}
	entries, _ := os.ReadDir(vmbundle.Path(data, "work"))
	if len(entries) != 0 {
		t.Errorf("the bundle holds %v", entries)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestHostFormat runs the steps with the real diskutil, confined by the
// real profile in a child, on a bundle under .scratch/ of the
// repository. It attaches, formats and mounts an image on the host, so
// it runs only with WB_DISKFMT_HOST_TEST=1, attended
// (docs/agents/host-work.md). It is the confined diskutil run of I228:
//
//	WB_DISKFMT_HOST_TEST=1 go test -run TestHostFormat -v ./cmd/wb-diskfmt
//
// It checks a formatted image at the final name, far fewer blocks than
// the nominal size, a canonical volume UUID, no device left attached,
// and a read outside the bundle denied.
func TestHostFormat(t *testing.T) {
	if os.Getenv("WB_DISKFMT_HOST_TEST") != "1" {
		t.Skip("attaches and formats an image on the host: set WB_DISKFMT_HOST_TEST=1 to run it, attended")
	}
	scratch, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".scratch"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	// Under .scratch/ rather than t.TempDir(), so the image is kept for
	// inspection after an attended run.
	data := filepath.Join(scratch, "diskfmt-host-test-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Logf("data directory %s, kept for inspection", data)
	bundle := vmbundle.Path(data, "work")
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(data, "outside")
	if err := os.WriteFile(outside, []byte("outside the bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShim(t, data, SlotEnv+"=work", childOutside+"="+outside)
	s.versions(testVersion)
	s.send(formatRequest(diskfmt.MinSizeGiB))
	formatted := s.recv(10 * time.Minute)
	uuid := formatted.GetFormatted().GetVolumeUuid()
	if !diskfmt.CanonicalUUID(uuid) {
		t.Fatalf("answer %v, want a canonical volume UUID\n%s", formatted, s.wait())
	}
	s.send(&diskfmtv1.HostMessage{Kind: &diskfmtv1.HostMessage_Rename{Rename: &diskfmtv1.RenameRequest{}}})
	renamed := s.recv(time.Minute)
	logs := s.wait()
	t.Logf("wb-diskfmt's log:\n%s", logs)
	if renamed.GetRenamed().GetFinalName() != vmbundle.DataDiskName {
		t.Fatalf("answer %v, want the final name", renamed)
	}
	checkHostResult(t, bundle, logs)
}

func checkHostResult(t *testing.T, bundle, logs string) {
	t.Helper()
	final := filepath.Join(bundle, vmbundle.DataDiskName)
	info, err := os.Lstat(final)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no stat for %s", final)
	}
	nominal := int64(diskfmt.MinSizeGiB) << 30
	used := st.Blocks * 512
	t.Logf("%s: size %d, %d bytes allocated", final, info.Size(), used)
	if info.Size() != nominal || used == 0 || used > nominal/100 {
		t.Errorf("size %d with %d bytes allocated, want %d, formatted and sparse", info.Size(), used, nominal)
	}
	if _, err := os.Lstat(filepath.Join(bundle, vmbundle.DataDiskTempName)); err == nil {
		t.Error("the temporary name is still there")
	}
	if !strings.Contains(logs, outsideDenied) {
		t.Errorf("the confined child read a file outside the bundle:\n%s", logs)
	}
	// hdiutil runs here, in the unconfined test, only to list what is
	// attached.
	out, err := exec.CommandContext(t.Context(), "/usr/bin/hdiutil", "info").CombinedOutput()
	if err != nil {
		t.Fatalf("hdiutil info: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte(bundle)) {
		t.Errorf("an image of the bundle is still attached:\n%s", out)
	}
}
