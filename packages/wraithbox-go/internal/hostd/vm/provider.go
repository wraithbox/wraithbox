package vm

import (
	"context"
	"net"

	vmdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/vmd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/datadisk"
)

// Provider starts one Instance for each VM run. On macOS each Instance
// is a new wb-vmd with a new hand-off socketpair (S04-04-vm-processes,
// "One `wb-vmd` per VM"). The interface is wb-hostd's, not
// internal/platform's: the platform boundary is wb-vmd's gRPC contract,
// proto/wraithbox/vmd/v1 (S12-01-platform-interfaces, "VM provider").
type Provider interface {
	// Open starts a new instance for slot. The slot is the only thing it
	// is given: on macOS it becomes the instance's WB_VMD_SLOT, and the
	// bundle path follows from it there (S04-04-vm-processes, "The
	// slot"). No request names a path, a slot or a device. The manager
	// passes a ctx that is never cancelled, so an instance may keep it for
	// its life, which ends at Close. The manager bounds its own wait for
	// Open, and closes an instance that opens after that wait ended.
	Open(ctx context.Context, slot Slot) (Instance, error)
}

// Instance is one VM run: one wb-vmd, and the VM of one generation.
// Every method but Close and States is a VMService call of the same
// name. A refusal of wb-vmd is a *ProviderRefusal.
type Instance interface {
	// Start boots the VM, and returns once it has started. A start the
	// framework failed returns a *ProviderRefusal whose VMError holds the
	// framework's error, from the state wb-vmd published with it.
	Start(ctx context.Context, req *vmdv1.StartRequest) error
	// Stop asks for a stop in req's mode. It returns without waiting for
	// the VM to stop: States says when it has.
	Stop(ctx context.Context, req *vmdv1.StopRequest) error
	// States sends the current state, then each change, as WatchState
	// does. It is closed when the instance ends: a wb-vmd that exits
	// means its VM stopped (S04-07-supervision, "`wb-vmd` and
	// `wb-vmd-install`"). The manager reads it until it is closed, Close
	// included.
	States() <-chan *vmdv1.VMState
	// ConnectGuest opens a connection to port in the guest of
	// generation, and returns the handed-off descriptor as a net.Conn.
	ConnectGuest(ctx context.Context, generation uint64, port uint32) (net.Conn, error)
	// Close ends the instance: on macOS it closes wb-vmd's lifeline,
	// which stops the VM at once, and waits for wb-vmd to exit. It is
	// safe to call more than once.
	Close() error
}

// ProviderRefusal is a refusal of the provider: wb-vmd's rule, from the
// google.rpc.ErrorInfo reason of its gRPC error (proto/wraithbox/vmd/v1).
// Every field is data a possibly compromised wb-vmd chose.
type ProviderRefusal struct {
	Rule   string
	Detail string
	// VMError is the framework error behind a failed start, nil when
	// there is none.
	VMError *vmdv1.VMError
}

func (p *ProviderRefusal) Error() string { return p.Rule + ": " + p.Detail }

// Cloner makes the VM bundle's copy of the guest tools disk
// (S06-12-guest-agent, "Guest tools disk"). On macOS it is an APFS
// clone, clonefile(2).
type Cloner interface {
	// Clone creates dst as a copy of src. It creates dst exclusively: it
	// fails when anything is at dst, a symbolic link included, and never
	// follows one there. It doesn't follow a symbolic link at src.
	// clonefile(2) with CLONE_NOFOLLOW does both. After a failure it may
	// leave a partial dst, which the caller deletes.
	Clone(src, dst string) error
}

// DataDisks is the data disk side of each VM. *datadisk.Manager
// implements it (S06-13-data-disk-format). Each method takes the slot's name, work
// or isolated, and returns a *datadisk.Refusal for a refusal.
type DataDisks interface {
	// Prepare runs the checks before a start, and makes a new disk when
	// the slot has none.
	Prepare(ctx context.Context, slot string) error
	// Started records that the VM started with its disk.
	Started(ctx context.Context, slot string) error
	// Reset replaces the slot's disk. The manager holds the slot's start
	// lock while it runs.
	Reset(ctx context.Context, slot string) (datadisk.ResetResult, error)
}

// Generations hands out VM generations. *state.DB implements it: each
// call commits the next generation of the slot before it returns, from
// 1 up, so a generation is never used twice, across restarts of
// wb-hostd too (S04-02-vm-provider, "Restore").
type Generations interface {
	NextGeneration(ctx context.Context, slot string) (uint64, error)
}
