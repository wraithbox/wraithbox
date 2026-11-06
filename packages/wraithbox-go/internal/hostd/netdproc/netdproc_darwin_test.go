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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

// buildNetd builds wb-netd stamped with nt.Own, once per test binary.
var buildNetd = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "netdproc")
	if err != nil {
		return "", err
	}
	buildDir = dir
	out := filepath.Join(dir, "wb-netd")
	cmd := exec.Command("go", "build", "-ldflags=-X "+version.StampVar+"="+nt.Own, "-o", out, "../../../cmd/wb-netd")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", errors.New(string(b))
	}
	return out, nil
})

type setup struct {
	starter *netdproc.Starter
	log     *syncBuffer
	netdLog *syncBuffer
	guest   *os.File
	t       transport
}

func newSetup(t *testing.T, program string) *setup {
	t.Helper()
	s := &setup{log: &syncBuffer{}, netdLog: &syncBuffer{}}
	logger := slog.New(slog.NewJSONHandler(s.log, nil))
	pol := &policy.Policy{Generation: 1, WildcardBudget: 10, Endpoints: []policy.Endpoint{{
		Source: policy.Source{Kind: policy.SourceGlobal}, RuleName: "api", Protocol: policy.ProtocolL4,
		Host: match.ExactHost(nt.Name("api.example.com")), Ports: []uint16{443},
	}}}
	var err error
	s.starter, err = netdproc.New(netdproc.Config{
		Program: program, Own: nt.Own, Log: logger,
		Logs:       func(string) io.Writer { return s.netdLog },
		Accounting: egress.NewState(egress.StateConfig{Log: logger}),
		Events:     events.NewHub(&audittest.Recorder{}, logger),
		Policy:     func(context.Context) (*policy.Policy, error) { return pol, nil },
		AckTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
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
	run, err := s.starter.Start(t.Context(), vm.SlotWork, 3, guestMAC, s.t)
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

// A wb-netd that a reader ends exits, and Exited says so: the VM
// manager then stops the VM (vm.RuleNetdExited).
func TestNetdEndedExits(t *testing.T) {
	s := newSetup(t, netd(t))
	run, err := s.starter.Start(t.Context(), vm.SlotIsolated, 1, guestMAC, s.t)
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
			run, err := s.starter.Start(t.Context(), vm.SlotWork, 1, c.mac, tr)
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
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(b.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in\n%s", s, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
