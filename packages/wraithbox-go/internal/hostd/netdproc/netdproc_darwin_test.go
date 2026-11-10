package netdproc_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/eventchan"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/egress"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/events"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/netdproc"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// syncBuffer is a log written by several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// transport is a packet transport that hands its descriptor to a
// child, as *vmhandoff.Transport does.
type transport struct{ f *os.File }

func (t transport) StartWith(cmd *exec.Cmd, index int) error {
	cmd.ExtraFiles[index] = t.f
	defer func() { cmd.ExtraFiles[index] = nil }()
	return cmd.Start()
}

func (t transport) Close() error { return t.f.Close() }

// closeOnly is a packet transport that can't hand its descriptor on.
type closeOnly struct{}

func (closeOnly) Close() error { return nil }

var guestMAC = []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}

// buildDir holds the wb-netd buildNetd builds. TestMain removes it.
var buildDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// makeBuildDir makes buildDir, once per test binary.
var makeBuildDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "netdproc")
	buildDir = dir
	return dir, err
})

// build builds the command in pkg as name, stamped with nt.Own, and with
// each of set as a -X flag of the linker.
func build(pkg, name string, set ...string) (string, error) {
	dir, err := makeBuildDir()
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, name)
	ldflags := "-X " + version.StampVar + "=" + nt.Own
	for _, v := range set {
		ldflags += " -X " + v
	}
	cmd := exec.CommandContext(context.Background(), "go", "build", "-ldflags="+ldflags, "-o", out, pkg)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", errors.New(string(b))
	}
	return out, nil
}

// buildNetd builds wb-netd, buildFlood testdata/flood-netd, and
// buildIgnoreStop flood-netd in the mode that ignores the stop, once per
// test binary.
var (
	buildNetd       = sync.OnceValues(func() (string, error) { return build("../../../cmd/wb-netd", "wb-netd") })
	buildFlood      = sync.OnceValues(func() (string, error) { return build("./testdata/flood-netd", "flood-netd") })
	buildIgnoreStop = sync.OnceValues(func() (string, error) {
		return build("./testdata/flood-netd", "flood-netd-ignore-stop", "main.mode=ignore-stop")
	})
)

type setup struct {
	starter *netdproc.Starter
	log     *syncBuffer
	netdLog *syncBuffer
	guest   *os.File
	t       transport
}

// newSetup starts a Starter of program. Each edit changes its Config.
func newSetup(t *testing.T, program string, edits ...func(*netdproc.Config)) *setup {
	t.Helper()
	s := &setup{log: &syncBuffer{}, netdLog: &syncBuffer{}}
	logger := slog.New(slog.NewJSONHandler(s.log, nil))
	pol := &policy.Policy{Generation: 1, WildcardBudget: 10, Endpoints: []policy.Endpoint{{
		Source: policy.Source{Kind: policy.SourceGlobal}, RuleName: "api", Protocol: policy.ProtocolL4,
		Host: match.ExactHost(nt.Name("api.example.com")), Ports: []uint16{443},
	}}}
	cfg := netdproc.Config{
		Program: program, Own: nt.Own, Log: logger,
		Logs:       func(string) io.Writer { return s.netdLog },
		Accounting: egress.NewState(egress.StateConfig{Log: logger}),
		Events:     events.NewHub(&audittest.Recorder{}, logger),
		Policy:     func(context.Context) (*policy.Policy, error) { return pol, nil },
		AckTimeout: 10 * time.Second,
	}
	for _, edit := range edits {
		edit(&cfg)
	}
	var err error
	s.starter, err = netdproc.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Closed on exec, as the hand-off receiver's transport is: wb-netd
	// refuses a descriptor beyond its start interface.
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	s.t = transport{os.NewFile(uintptr(fds[0]), "host")}
	s.guest = os.NewFile(uintptr(fds[1]), "guest")
	t.Cleanup(func() {
		_ = s.t.Close()
		_ = s.guest.Close()
	})
	return s
}

func netd(t *testing.T) string {
	t.Helper()
	p, err := buildNetd()
	if err != nil {
		t.Fatalf("build wb-netd: %v", err)
	}
	return p
}

// TestC11NetdStartAndStop is C11-host-daemons, "`wb-netd`": wb-hostd
// starts a confined wb-netd with the start interface, which takes the
// start message and acknowledges the policy, and at the stop closes its
// control channel, after which it exits 0 with netd-stopped.
func TestC11NetdStartAndStop(t *testing.T) {
	s := newSetup(t, netd(t))
	run, err := s.start(t.Context(), vm.SlotWork, 3, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s\n%s", err, s.log, s.netdLog)
	}
	select {
	case <-run.Exited():
		t.Fatalf("wb-netd exited\n%s", s.netdLog)
	default:
	}
	_ = run.Stop()
	<-run.Exited()
	if !strings.Contains(s.log.String(), `"state":"exit status 0"`) {
		t.Errorf("wb-netd didn't exit 0\n%s", s.log)
	}
	waitFor(t, s.netdLog, `netd-stopped`)
}

// A descriptor wb-hostd inherited without close-on-exec doesn't reach
// wb-netd, which would refuse it: the spawn marks it, with
// inherited-descriptor, and wb-netd starts.
func TestInheritedDescriptorNotPassed(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = r.Close(), w.Close() }()
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, w.Fd(), syscall.F_SETFD, 0); errno != 0 {
		t.Fatal(errno)
	}
	s := newSetup(t, netd(t))
	run, err := s.start(t.Context(), vm.SlotWork, 1, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s\n%s", err, s.log, s.netdLog)
	}
	_ = run.Stop()
	if !strings.Contains(s.log.String(), `"rule":"inherited-descriptor","decision":"close-on-exec"`) {
		t.Errorf("the mark isn't logged\n%s", s.log)
	}
}

// A wb-netd that a reader ends exits, and Exited says so: the VM
// manager then stops the VM (vm.RuleNetdExited).
func TestNetdEndedExits(t *testing.T) {
	s := newSetup(t, netd(t))
	run, err := s.start(t.Context(), vm.SlotIsolated, 1, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, s.netdLog)
	}
	ender, ok := run.(interface{ End(rule string) })
	if !ok {
		t.Fatalf("%T has no End", run)
	}
	ender.End(netdproc.RuleEnded)
	select {
	case <-run.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("wb-netd didn't exit")
	}
	_ = run.Stop()
	if !strings.Contains(s.log.String(), `"rule":"netd-ended","decision":"end-netd"`) {
		t.Errorf("the end isn't logged\n%s", s.log)
	}
}

// Each start refused before or after the spawn fails closed with
// netd-start-failed: no NetworkRun is returned.
func TestStartRefused(t *testing.T) {
	cases := []struct {
		name    string
		program func(t *testing.T) string
		mac     []byte
		noFile  bool
	}{
		{"no descriptor", netd, guestMAC, true},
		{"multicast MAC", netd, []byte{1, 2, 3, 4, 5, 6}, false},
		{"short MAC", netd, []byte{2, 2, 3}, false},
		// A program that exits at once sends no version record.
		{"not wb-netd", func(*testing.T) string { return "/usr/bin/true" }, guestMAC, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSetup(t, c.program(t))
			var tr vm.PacketTransport = s.t
			if c.noFile {
				tr = closeOnly{}
			}
			run, err := s.start(t.Context(), vm.SlotWork, 1, c.mac, tr)
			if err == nil || run != nil {
				t.Fatalf("start = %v, %v; want a refusal", run, err)
			}
			if !strings.Contains(s.log.String(), `"rule":"netd-start-failed","decision":"refuse"`) {
				t.Errorf("the refusal isn't logged\n%s", s.log)
			}
		})
	}
}

func TestLink(t *testing.T) {
	l, err := netdproc.Link(guestMAC)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(l.GuestMAC, l.GatewayMAC) || l.GatewayMAC[0]&1 != 0 || !l.Subnet.Contains(l.Leased) {
		t.Fatalf("link %+v", l)
	}
}

func waitFor(t *testing.T, b *syncBuffer, s string) {
	t.Helper()
	waitWithin(t, b, s, 5*time.Second)
}

// waitWithin waits until b holds s, and fails the test after d.
func waitWithin(t *testing.T, b *syncBuffer, s string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !strings.Contains(b.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in\n%s", s, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// start prepares and starts a wb-netd, as the VM manager does.
func (s *setup) start(ctx context.Context, slot vm.Slot, generation uint64, mac []byte, t vm.PacketTransport) (vm.NetworkRun, error) {
	prep, err := s.starter.Prepare(ctx, slot, generation, mac)
	if err != nil {
		return nil, err
	}
	return prep.Start(ctx, t)
}

// pid is the process ID of the wb-netd that started, from the log.
func (s *setup) pid(t *testing.T) int {
	t.Helper()
	m := regexp.MustCompile(`"msg":"wb-netd started".*"pid":(\d+)`).FindStringSubmatch(s.log.String())
	if m == nil {
		t.Fatalf("no pid logged\n%s", s.log)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// A wb-netd that doesn't exit once its control channel closed, here a
// stopped one, is killed after StopTimeout with netd-stop-timeout.
func TestNetdStopTimeoutKills(t *testing.T) {
	s := newSetup(t, netd(t), func(c *netdproc.Config) { c.StopTimeout = 100 * time.Millisecond })
	run, err := s.start(t.Context(), vm.SlotWork, 1, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, s.netdLog)
	}
	if err := syscall.Kill(s.pid(t), syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	_ = run.Stop()
	<-run.Exited()
	if !strings.Contains(s.log.String(), `"rule":"netd-stop-timeout","decision":"kill"`) {
		t.Errorf("the kill isn't logged\n%s", s.log)
	}
}

// fullLog is an audit writer that holds its cap of records for good, as
// one whose disk is slow or whose writes fail.
type fullLog struct{ *audittest.Recorder }

func (fullLog) Room() <-chan struct{} { return make(chan struct{}) }

// TestStopReadsPastAFullQueue is S04-08-records-and-bounds, "Host work
// the guest can cause": a wb-netd whose event channel is full while the
// audit writer holds its cap still hands its open runs to the stop. The
// stop lets the receiver read past its hold, so wb-netd's close drops no
// record, and the stop doesn't wait for StopTimeout.
func TestStopReadsPastAFullQueue(t *testing.T) {
	const stopTimeout = 2 * time.Second
	flood, err := buildFlood()
	if err != nil {
		t.Fatalf("build flood-netd: %v", err)
	}
	s := newSetup(t, flood, func(c *netdproc.Config) {
		c.Events = events.NewHub(fullLog{&audittest.Recorder{}}, c.Log)
		c.StopTimeout = stopTimeout
	})
	run, err := s.start(t.Context(), vm.SlotWork, 1, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s\n%s", err, s.log, s.netdLog)
	}
	waitFor(t, s.netdLog, "flood-netd blocked")
	began := time.Now()
	runs := run.Stop()
	if took := time.Since(began); took >= stopTimeout || strings.Contains(s.log.String(), "netd-stop-timeout") {
		t.Errorf("the stop took %s\n%s", took, s.log)
	}
	if !slices.ContainsFunc(runs, func(r audit.OpenRun) bool { return r.Rule == "frame-ethertype" }) {
		t.Errorf("open runs %+v, want frame-ethertype", runs)
	}
	if strings.Contains(s.netdLog.String(), "event records not written at close") {
		t.Errorf("flood-netd dropped records\n%s", s.netdLog)
	}
}

// TestStopBoundsASenderThatIgnoresIt is S09-11-audit, "Telling the
// senders": a wb-netd that keeps sending after its control channel
// closed gets no more than the stop allowance read past the hold. Once
// it has written past the allowance it is killed, the receiver logs the
// records past the allowance with event-stop-allowance, and the stop
// returns. The test kills it, not the stop at StopTimeout, so the
// outcome doesn't depend on how fast it writes: TestNetdStopTimeoutKills
// covers the stop's kill.
func TestStopBoundsASenderThatIgnoresIt(t *testing.T) {
	// The stop's own kill must not come first. The wait for the sender
	// fails the test before it does.
	const stopTimeout, waitSender = 2 * time.Minute, time.Minute
	// The allowance is events.stopAllowance. The slack is the records
	// read before the hold began, as flood-netd's pastAllowance counts.
	const allowance, slack = 2 * eventchan.DefaultQueueCap, 2
	program, err := buildIgnoreStop()
	if err != nil {
		t.Fatalf("build flood-netd: %v", err)
	}
	rec := &audittest.Recorder{}
	s := newSetup(t, program, func(c *netdproc.Config) {
		c.Events = events.NewHub(fullLog{rec}, c.Log)
		c.StopTimeout = stopTimeout
	})
	run, err := s.start(t.Context(), vm.SlotWork, 1, guestMAC, s.t)
	if err != nil {
		t.Fatalf("start: %v\n%s\n%s", err, s.log, s.netdLog)
	}
	waitFor(t, s.netdLog, "flood-netd blocked")
	if n := len(rec.Records()); n > slack {
		t.Fatalf("%d records read before the stop, more than the slack of %d", n, slack)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		run.Stop()
	}()
	// What it wrote and the receiver didn't read stays in the socket
	// after the kill, so the drain finds a record past the allowance.
	waitWithin(t, s.netdLog, "flood-netd wrote past the allowance", waitSender)
	if err := syscall.Kill(s.pid(t), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatalf("the stop didn't return\n%s", s.log)
	}
	if logged, want := s.log.String(), `"rule":"event-stop-allowance","decision":"drop"`; !strings.Contains(logged, want) {
		t.Errorf("no %s in the log\n%s", want, logged)
	}
	if n := len(rec.Records()); n > allowance+slack {
		t.Errorf("%d records read, more than the allowance of %d", n, allowance)
	}
}
