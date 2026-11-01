package hostservice_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/config"
	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/audit/audittest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk/datadisktest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/hostservice"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/startup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm/vmtest"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeVMs records the slots it was called for, and answers with err.
type fakeVMs struct {
	mu     sync.Mutex
	calls  []string
	err    error
	status vm.Status
}

func (f *fakeVMs) record(op string, slot vm.Slot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op+" "+string(slot))
	return f.err
}

func (f *fakeVMs) StartRun(_ context.Context, slot vm.Slot) (uint64, error) {
	if err := f.record("start", slot); err != nil {
		return 0, err
	}
	return 5, nil
}
func (f *fakeVMs) Stop(_ context.Context, slot vm.Slot) error { return f.record("stop", slot) }

func (f *fakeVMs) Status(slot vm.Slot) vm.Status {
	_ = f.record("status", slot)
	s := f.status
	s.Slot = slot
	return s
}

func (f *fakeVMs) ResetData(_ context.Context, slot vm.Slot) (datadisk.ResetResult, error) {
	if err := f.record("reset", slot); err != nil {
		return datadisk.ResetResult{}, err
	}
	return datadisk.ResetResult{OldVolumeUUID: "A", NewVolumeUUID: "B"}, nil
}

func (f *fakeVMs) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakeLinks holds a link state per slot, which the test sets.
type fakeLinks struct {
	mu     sync.Mutex
	status map[vm.Slot]guestconn.Status
}

func (f *fakeLinks) Status(slot vm.Slot) guestconn.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.status[slot]; ok {
		return s
	}
	return guestconn.Status{Slot: slot, State: guestconn.StateDown}
}

func (f *fakeLinks) set(s guestconn.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status == nil {
		f.status = map[vm.Slot]guestconn.Status{}
	}
	f.status[s.Slot] = s
}

// newManager is the VM manager on a temporary <data>, with the fake
// provider and a fake wb-diskfmt.
func newManager(t *testing.T, log *slog.Logger, sessions func(vm.Slot) []vm.Session) *vm.Manager {
	t.Helper()
	rec := &audittest.Recorder{}
	data := t.TempDir()
	db, err := state.Open(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	disks, err := datadisk.New(datadisk.Config{
		DataDir: data, Records: db, Audit: rec, Logger: log,
		Shim: &datadisktest.Shim{DataDir: data, UUIDs: []string{"0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(t.TempDir(), "tools.img")
	if err := os.WriteFile(tools, []byte("tools"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := vm.New(vm.Config{
		DataDir: data, ToolsImage: tools, Resources: config.Defaults(), Generations: db, MACAddresses: db,
		Provider: &vmtest.Provider{}, Cloner: &vmtest.CopyCloner{}, DataDisks: disks,
		Sessions: sessions, Audit: rec, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// serve serves s over an in-memory connection and returns a client.
func serve(t *testing.T, s *hostservice.Server) hostdv1.HostServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	hostdv1.RegisterHostServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hostdv1.NewHostServiceClient(conn)
}

func ruleOf(t *testing.T, err error) (codes.Code, string, string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || err == nil {
		t.Fatalf("err %v isn't a status", err)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if info.GetDomain() != hostservice.Domain {
				t.Errorf("domain %s", info.GetDomain())
			}
			return st.Code(), info.GetReason(), st.Message()
		}
	}
	t.Fatalf("status %v has no ErrorInfo", st)
	return 0, "", ""
}

func getStatus(t *testing.T, c hostdv1.HostServiceClient, v hostdv1.VM) *hostdv1.VMStatus {
	t.Helper()
	res, err := c.GetVMStatus(t.Context(), &hostdv1.GetVMStatusRequest{Vm: v})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetStatus()
}

// TestStartStatusStop: a start, the link's states as status reads them,
// and a stop, through the real manager and the fake provider.
func TestStartStatusStop(t *testing.T) {
	links := &fakeLinks{}
	c := serve(t, &hostservice.Server{VMs: newManager(t, slog.New(slog.DiscardHandler), nil), Links: links})
	work := hostdv1.VM_VM_WORK

	stopped, running := hostdv1.VMPhase_VM_PHASE_STOPPED, hostdv1.VMPhase_VM_PHASE_RUNNING
	wantState(t, "before the start", getStatus(t, c, work), stopped, 0, hostdv1.LinkState_LINK_STATE_DOWN)
	started, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: work})
	if err != nil {
		t.Fatal(err)
	}
	g := started.GetGeneration()
	if g == 0 {
		t.Fatal("generation 0")
	}
	wantState(t, "no link yet", getStatus(t, c, work), running, g, hostdv1.LinkState_LINK_STATE_CONNECTING)

	links.set(guestconn.Status{Slot: vm.SlotWork, Generation: g, State: guestconn.StateLinked, GuestVersion: "1.2.3"})
	s := getStatus(t, c, work)
	wantState(t, "linked", s, running, g, hostdv1.LinkState_LINK_STATE_LINKED)
	if s.GetGuestVersion() != "1.2.3" {
		t.Errorf("guest version %q", s.GetGuestVersion())
	}
	// A link state of an older run is not this run's.
	links.set(guestconn.Status{Slot: vm.SlotWork, Generation: g - 1, State: guestconn.StateLinked, GuestVersion: "1.2.3"})
	wantState(t, "an older run's link", getStatus(t, c, work), running, g, hostdv1.LinkState_LINK_STATE_CONNECTING)
	wantState(t, "isolated", getStatus(t, c, hostdv1.VM_VM_ISOLATED), stopped, 0, hostdv1.LinkState_LINK_STATE_DOWN)

	if _, err := c.StopVM(t.Context(), &hostdv1.StopVMRequest{Vm: work}); err != nil {
		t.Fatal(err)
	}
	wantState(t, "after the stop", getStatus(t, c, work), stopped, g, hostdv1.LinkState_LINK_STATE_DOWN)
}

// wantState checks a status's phase, generation and link state.
func wantState(t *testing.T, when string, s *hostdv1.VMStatus, phase hostdv1.VMPhase, generation uint64, link hostdv1.LinkState) {
	t.Helper()
	if s.GetPhase() != phase || s.GetGeneration() != generation || s.GetLinkState() != link {
		t.Fatalf("%s: %v, want %s, generation %d, link %s", when, s, phase, generation, link)
	}
}

// TestStatusLinkRefused: a refused hello reaches the status with its
// rule, both versions and its message.
func TestStatusLinkRefused(t *testing.T) {
	links := &fakeLinks{}
	c := serve(t, &hostservice.Server{VMs: newManager(t, slog.New(slog.DiscardHandler), nil), Links: links})
	started, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK})
	if err != nil {
		t.Fatal(err)
	}
	g := started.GetGeneration()
	refused := &guestconn.HelloRefusal{Rule: "peer-version-mismatch", OwnVersion: `"1.2.3"`, GuestVersion: `"9.9.9"`, Message: "stop and start"}
	links.set(guestconn.Status{Slot: vm.SlotWork, Generation: g, State: guestconn.StateRefused, Refusal: refused})
	s := getStatus(t, c, hostdv1.VM_VM_WORK)
	wantState(t, "refused", s, hostdv1.VMPhase_VM_PHASE_RUNNING, g, hostdv1.LinkState_LINK_STATE_REFUSED)
	r := s.GetLinkRefusal()
	if r.GetRule() != "peer-version-mismatch" || r.GetGuestVersion() != `"9.9.9"` || r.GetHostdVersion() != `"1.2.3"` || r.GetMessage() != "stop and start" {
		t.Errorf("refusal %v", r)
	}
}

// TestStatusAuditRefusal: the audit log's refusal reaches each VM's
// status with its rule and message, stopped or running, and leaves it
// once the refusal ends (S09-11-audit, "Free space and failed writes").
func TestStatusAuditRefusal(t *testing.T) {
	rec := &audittest.Recorder{}
	c := serve(t, &hostservice.Server{VMs: newManager(t, slog.New(slog.DiscardHandler), nil), Links: &fakeLinks{}, Audit: rec})
	if _, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK}); err != nil {
		t.Fatal(err)
	}
	if r := getStatus(t, c, hostdv1.VM_VM_WORK).GetAuditRefusal(); r != nil {
		t.Errorf("a refusal while the log takes records: %v", r)
	}
	rec.Refuse(audit.RuleFreeSpace, "the volume is below the floor")
	for _, v := range []hostdv1.VM{hostdv1.VM_VM_WORK, hostdv1.VM_VM_ISOLATED} {
		r := getStatus(t, c, v).GetAuditRefusal()
		if r.GetRule() != audit.RuleFreeSpace || r.GetMessage() != "the volume is below the floor" || r.GetUntilUnix() != 0 || !r.GetSessions() {
			t.Errorf("%s: refusal %v, want %s", v, r, audit.RuleFreeSpace)
		}
	}
	rec.Refuse("", "")
	if r := getStatus(t, c, hostdv1.VM_VM_ISOLATED).GetAuditRefusal(); r != nil {
		t.Errorf("a refusal after it ended: %v", r)
	}
}

// TestStatusAuditBudget: each VM's status shows its own use of the audit
// budgets, and the rule and its end while it is over one (S09-11-audit,
// "Budgets").
func TestStatusAuditBudget(t *testing.T) {
	rec := &audittest.Recorder{}
	until := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	rec.SetBudget("work", audit.Budget{
		Rule: audit.RuleBytes, Until: until, EventsThisMinute: 7, EventsPerMinute: 6000, BytesToday: 65 << 20, BytesPerDay: 64 << 20,
	})
	rec.SetBudget("isolated", audit.Budget{EventsThisMinute: 1, EventsPerMinute: 6000, BytesPerDay: 64 << 20})
	c := serve(t, &hostservice.Server{VMs: newManager(t, slog.New(slog.DiscardHandler), nil), Links: &fakeLinks{}, Audit: rec})
	b := getStatus(t, c, hostdv1.VM_VM_WORK).GetAuditBudget()
	if b.GetRule() != audit.RuleBytes || b.GetUntilUnix() != until.Unix() || b.GetEventsThisMinute() != 7 ||
		b.GetEventsPerMinute() != 6000 || b.GetBytesToday() != 65<<20 || b.GetBytesPerDay() != 64<<20 {
		t.Errorf("work: budget %v", b)
	}
	b = getStatus(t, c, hostdv1.VM_VM_ISOLATED).GetAuditBudget()
	if b.GetRule() != "" || b.GetUntilUnix() != 0 || b.GetEventsThisMinute() != 1 {
		t.Errorf("isolated: budget %v", b)
	}
}

// TestStatusDataMount: the data mount of the current generation reaches
// the status with its rule and message, and one of an older run's
// doesn't.
func TestStatusDataMount(t *testing.T) {
	links := &fakeLinks{}
	c := serve(t, &hostservice.Server{VMs: newManager(t, slog.New(slog.DiscardHandler), nil), Links: links})
	started, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK})
	if err != nil {
		t.Fatal(err)
	}
	g := started.GetGeneration()
	if s := getStatus(t, c, hostdv1.VM_VM_WORK); s.GetDataMount() != nil {
		t.Errorf("data mount before the link started: %v", s.GetDataMount())
	}
	tests := []struct {
		dm   guestconn.DataMount
		want hostdv1.DataMountState
	}{
		{guestconn.DataMount{State: guestconn.MountPending}, hostdv1.DataMountState_DATA_MOUNT_STATE_PENDING},
		{guestconn.DataMount{State: guestconn.MountMounted}, hostdv1.DataMountState_DATA_MOUNT_STATE_MOUNTED},
		{guestconn.DataMount{State: guestconn.MountRefused, Rule: "data-mount-flags", Message: "stop and reset"}, hostdv1.DataMountState_DATA_MOUNT_STATE_REFUSED},
	}
	for _, tt := range tests {
		links.set(guestconn.Status{Slot: vm.SlotWork, Generation: g, State: guestconn.StateLinked, DataMount: tt.dm})
		dm := getStatus(t, c, hostdv1.VM_VM_WORK).GetDataMount()
		if dm.GetState() != tt.want || dm.GetRule() != tt.dm.Rule || dm.GetMessage() != tt.dm.Message {
			t.Errorf("data mount %v, want %s %q %q", dm, tt.want, tt.dm.Rule, tt.dm.Message)
		}
	}
	links.set(guestconn.Status{Slot: vm.SlotWork, Generation: g - 1, State: guestconn.StateLinked, DataMount: tests[2].dm})
	if dm := getStatus(t, c, hostdv1.VM_VM_WORK).GetDataMount(); dm != nil {
		t.Errorf("an older run's data mount: %v", dm)
	}
}

// TestManagerRefusals: the manager's refusals arrive with their rule as
// the ErrorInfo reason, and the message names the live sessions.
func TestManagerRefusals(t *testing.T) {
	var live []vm.Session
	var mu sync.Mutex
	m := newManager(t, slog.New(slog.DiscardHandler), func(vm.Slot) []vm.Session {
		mu.Lock()
		defer mu.Unlock()
		return live
	})
	c := serve(t, &hostservice.Server{VMs: m})
	work := hostdv1.VM_VM_WORK

	_, err := c.StopVM(t.Context(), &hostdv1.StopVMRequest{Vm: work})
	if code, rule, _ := ruleOf(t, err); code != codes.FailedPrecondition || rule != string(vm.RuleNotRunning) {
		t.Errorf("stop of a stopped VM: %s %s", code, rule)
	}
	if _, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: work}); err != nil {
		t.Fatal(err)
	}
	_, err = c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: work})
	if _, rule, _ := ruleOf(t, err); rule != string(vm.RuleRunning) {
		t.Errorf("second start: %s", rule)
	}
	mu.Lock()
	live = []vm.Session{{ID: "s-1", State: vm.SessionRunning, PID: 4242}}
	mu.Unlock()
	_, err = c.StopVM(t.Context(), &hostdv1.StopVMRequest{Vm: work})
	_, rule, msg := ruleOf(t, err)
	if rule != string(vm.RuleLiveSessions) || !strings.Contains(msg, "s-1") || !strings.Contains(msg, "4242") {
		t.Errorf("stop with a session: %s %q", rule, msg)
	}
}

// TestRefusals: a request without a VM, and each kind of the manager's
// error, arrive with the rule as the ErrorInfo reason, for every call.
func TestRefusals(t *testing.T) {
	calls := map[string]func(hostdv1.HostServiceClient, hostdv1.VM) error{
		"StartVM": func(c hostdv1.HostServiceClient, v hostdv1.VM) error {
			_, err := c.StartVM(context.Background(), &hostdv1.StartVMRequest{Vm: v})
			return err
		},
		"StopVM": func(c hostdv1.HostServiceClient, v hostdv1.VM) error {
			_, err := c.StopVM(context.Background(), &hostdv1.StopVMRequest{Vm: v})
			return err
		},
		"ResetVMData": func(c hostdv1.HostServiceClient, v hostdv1.VM) error {
			_, err := c.ResetVMData(context.Background(), &hostdv1.ResetVMDataRequest{Vm: v})
			return err
		},
	}
	for _, tc := range []struct {
		name string
		vm   hostdv1.VM
		err  error
		code codes.Code
		rule string
	}{
		{"unspecified", hostdv1.VM_VM_UNSPECIFIED, nil, codes.InvalidArgument, hostservice.RuleVMUnspecified},
		{"out of the enum", hostdv1.VM(7), nil, codes.InvalidArgument, hostservice.RuleVMUnspecified},
		{"manager rule", hostdv1.VM_VM_WORK, &vm.Refusal{Rule: vm.RuleStartTimeout, Message: "x"}, codes.FailedPrecondition, string(vm.RuleStartTimeout)},
		{"wb-vmd rule", hostdv1.VM_VM_WORK, &vm.Refusal{Rule: "vmd-bundle-invalid", Message: "x"}, codes.FailedPrecondition, "vmd-bundle-invalid"},
		{"not a refusal", hostdv1.VM_VM_WORK, errors.New("boom"), codes.Internal, hostservice.RuleInternal},
	} {
		for method, call := range calls {
			t.Run(tc.name+" "+method, func(t *testing.T) {
				vms := &fakeVMs{err: tc.err}
				code, rule, _ := ruleOf(t, call(serve(t, &hostservice.Server{VMs: vms, Log: slog.New(slog.DiscardHandler)}), tc.vm))
				if code != tc.code || rule != tc.rule {
					t.Errorf("code %s rule %s, want %s %s", code, rule, tc.code, tc.rule)
				}
				if tc.err == nil && len(vms.Calls()) != 0 {
					t.Errorf("the manager was called: %v", vms.Calls())
				}
			})
		}
	}
	_, err := serve(t, &hostservice.Server{VMs: &fakeVMs{}}).GetVMStatus(t.Context(), &hostdv1.GetVMStatusRequest{})
	if _, rule, _ := ruleOf(t, err); rule != hostservice.RuleVMUnspecified {
		t.Errorf("status without a VM: %s", rule)
	}
}

// TestRefusingMode: in refusing mode every call is refused with the
// rule that put wb-hostd in it, the manager isn't asked, and the
// refusal is logged with its rule (S17-05-upgrades-and-migrations,
// "Refusing mode").
func TestRefusingMode(t *testing.T) {
	var logs syncBuffer
	vms := &fakeVMs{}
	c := serve(t, &hostservice.Server{
		VMs:      vms,
		Refusing: &startup.Refusing{Rule: "state-db-newer", Detail: "state.db is version 9, and wb-hostd knows up to 3"},
		Log:      slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	work := hostdv1.VM_VM_WORK
	for name, call := range map[string]func() error{
		"StartVM": func() error { _, err := c.StartVM(t.Context(), &hostdv1.StartVMRequest{Vm: work}); return err },
		"StopVM":  func() error { _, err := c.StopVM(t.Context(), &hostdv1.StopVMRequest{Vm: work}); return err },
		"GetVMStatus": func() error {
			_, err := c.GetVMStatus(t.Context(), &hostdv1.GetVMStatusRequest{Vm: work})
			return err
		},
		"ResetVMData": func() error {
			_, err := c.ResetVMData(t.Context(), &hostdv1.ResetVMDataRequest{Vm: work})
			return err
		},
	} {
		code, rule, msg := ruleOf(t, call())
		if code != codes.FailedPrecondition || rule != "state-db-newer" || !strings.Contains(msg, "version 9") {
			t.Errorf("%s: %s %s %q", name, code, rule, msg)
		}
		if !strings.Contains(logs.String(), `"method":"`+name+`"`) {
			t.Errorf("%s: the refusal isn't logged", name)
		}
	}
	if got := vms.Calls(); len(got) != 0 {
		t.Errorf("the manager was called: %v", got)
	}
	if !strings.Contains(logs.String(), `"rule":"state-db-newer"`) {
		t.Errorf("log %s", logs.String())
	}
}

// TestLastError: a framework error of the last run reaches the status.
func TestLastError(t *testing.T) {
	vms := &fakeVMs{status: vm.Status{
		Phase: vm.PhaseStopped, Generation: 3,
		LastError: &vmdv1.VMError{Code: 6, FailureReason: "limit", Errno: 1, Description: "too many VMs"},
	}}
	s := getStatus(t, serve(t, &hostservice.Server{VMs: vms}), hostdv1.VM_VM_ISOLATED)
	e := s.GetLastError()
	if e.GetCode() != 6 || e.GetFailureReason() != "limit" || e.GetErrno() != 1 || e.GetDescription() != "too many VMs" {
		t.Errorf("last error %v", e)
	}
	if got := vms.Calls(); len(got) != 1 || got[0] != "status isolated" {
		t.Errorf("calls %v", got)
	}
}

func TestResetVMData(t *testing.T) {
	vms := &fakeVMs{}
	res, err := serve(t, &hostservice.Server{VMs: vms}).ResetVMData(t.Context(), &hostdv1.ResetVMDataRequest{Vm: hostdv1.VM_VM_ISOLATED})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetOldVolumeUuid() != "A" || res.GetNewVolumeUuid() != "B" || len(vms.Calls()) != 1 || vms.Calls()[0] != "reset isolated" {
		t.Errorf("response %v, calls %v", res, vms.Calls())
	}
}

// ctxVMs records whether the context of a start was cancelled.
type ctxVMs struct {
	fakeVMs
	cancelled chan bool
}

func (c *ctxVMs) StartRun(ctx context.Context, slot vm.Slot) (uint64, error) {
	c.cancelled <- ctx.Err() != nil
	return c.fakeVMs.StartRun(ctx, slot)
}

// TestStartOutlivesCaller: a start that began runs on when its caller
// goes away, as a stop and a reset do.
func TestStartOutlivesCaller(t *testing.T) {
	vms := &ctxVMs{cancelled: make(chan bool, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res, err := (&hostservice.Server{VMs: vms}).StartVM(ctx, &hostdv1.StartVMRequest{Vm: hostdv1.VM_VM_WORK})
	if err != nil || res.GetGeneration() != 5 {
		t.Fatalf("StartVM: %v, %v", res, err)
	}
	if <-vms.cancelled {
		t.Error("the manager got the caller's cancelled context")
	}
}
