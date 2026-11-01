package vmdlink

import (
	"context"
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/vmhandoff"
)

// startVMD serves Start as wb-vmd does: it sends the host end of a new
// datagram socketpair in a KIND_NETWORK record before it answers. The
// fields change what it sends.
type startVMD struct {
	fakeVMD

	// refuse, when set, is the rule Start is refused with, after the
	// record is sent.
	refuse string
	// silent answers without a record.
	silent bool
	// stream sends a stream socket in place of a datagram one.
	stream bool
	// generation, when set, replaces the record's generation.
	generation uint64
}

func (v *startVMD) Start(_ context.Context, req *vmdv1.StartRequest) (*vmdv1.StartResponse, error) {
	if !v.silent {
		if err := v.sendTransport(req.GetGeneration()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if v.refuse != "" {
		return nil, status.Error(codes.FailedPrecondition, v.refuse)
	}
	return &vmdv1.StartResponse{}, nil
}

func (v *startVMD) sendTransport(generation uint64) error {
	typ := syscall.SOCK_DGRAM
	if v.stream {
		typ = syscall.SOCK_STREAM
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, typ, 0)
	if err != nil {
		return err
	}
	host := os.NewFile(uintptr(fds[0]), "host")
	guest := os.NewFile(uintptr(fds[1]), "guest")
	defer func() { _ = host.Close(); _ = guest.Close() }()
	if v.generation != 0 {
		generation = v.generation
	}
	record, err := proto.Marshal(&vmdv1.HandoffMessage{Generation: generation, Kind: vmdv1.Kind_KIND_NETWORK})
	if err != nil {
		return err
	}
	return v.tx.Send(record, host)
}

// newStartLink serves vmd, and returns a link to it.
func newStartLink(t *testing.T, vmd *startVMD) *Link {
	t.Helper()
	return linkTo(t, vmd, &vmd.fakeVMD)
}

// openFDs returns the descriptors open in this process.
func openFDs() []int {
	var open []int
	for fd := range 4096 {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			open = append(open, fd)
		}
	}
	return open
}

// requireNoNewFDs waits until no descriptor is open that wasn't in
// before: a refused record's descriptor is closed by the receiver,
// which may run a moment after the caller has its refusal.
func requireNoNewFDs(t *testing.T, before []int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var extra []int
		for _, fd := range openFDs() {
			if !slices.Contains(before, fd) {
				extra = append(extra, fd)
			}
		}
		if len(extra) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descriptors %v left open", extra)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStartTransport is C11-host-daemons, "Hand-off receiver": a Start
// returns the packet transport wb-vmd sent for its generation
// (S07-02-packet-path, "Packet transport").
func TestStartTransport(t *testing.T) {
	l := newStartLink(t, &startVMD{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tr, err := l.Start(ctx, &vmdv1.StartRequest{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tr == nil {
		t.Fatal("Start returned no transport")
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestStartRefusals: a Start whose transport is refused or missing is
// refused with the hand-off's rule, a refused Start returns wb-vmd's
// error, and none of them leaves a descriptor open.
func TestStartRefusals(t *testing.T) {
	cases := []struct {
		name string
		vmd  *startVMD
		rule string
	}{
		{"no record", &startVMD{silent: true}, RuleHandoffMissing},
		{"a stream socket", &startVMD{stream: true}, string(vmhandoff.RuleNetwork)},
		// The receiver refuses a record of another generation, and the
		// Start still waits for its own.
		{"another generation", &startVMD{generation: 2}, RuleHandoffMissing},
		{"wb-vmd refuses", &startVMD{refuse: "vm-start-failed"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newStartLink(t, c.vmd)
			before := openFDs()
			ctx, cancel := context.WithTimeout(t.Context(), 2*HandoffTimeout)
			defer cancel()
			tr, err := l.Start(ctx, &vmdv1.StartRequest{Generation: 1})
			if tr != nil {
				_ = tr.Close()
				t.Fatal("Start returned a transport")
			}
			requireStartError(t, err, c.rule)
			requireNoNewFDs(t, before)
		})
	}
}

func requireStartError(t *testing.T, err error, rule string) {
	t.Helper()
	r, isRefusal := errors.AsType[*Refusal](err)
	if rule == "" {
		if isRefusal || status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Start = %v, want wb-vmd's error as it is", err)
		}
		return
	}
	if !isRefusal || r.Rule != rule {
		t.Fatalf("Start = %v, want rule %s", err, rule)
	}
}
