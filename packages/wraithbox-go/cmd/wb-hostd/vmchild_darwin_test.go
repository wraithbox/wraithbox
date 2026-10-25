package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// The variables that make the test binary a wb-hostd child of the
// needs-vm test (vm_darwin_test.go).
const (
	// childVar is the path the child takes as wb-hostd's own: a built
	// bundle's Contents/MacOS/wb-hostd.
	childVar = "WB_HOSTD_TEST_CHILD"
	// configVar and logsVar are the child's <config> and <logs>. Its
	// <data> is the real one, the only one wb-vmd boots from.
	configVar = "WB_HOSTD_TEST_CONFIG"
	logsVar   = "WB_HOSTD_TEST_LOGS"
)

// TestMain runs the wb-hostd child when childVar is set, and the tests
// otherwise.
func TestMain(m *testing.M) {
	if exe := os.Getenv(childVar); exe != "" {
		os.Exit(runChild(exe))
	}
	os.Exit(m.Run())
}

// runChild runs wb-hostd as main does, with the test's <config> and
// <logs>, until SIGTERM. SIGUSR1 closes wb-hostd's end of the last
// connection to a guest: the test hook of the reconnect.
func runChild(exe string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	p, err := paths.Current(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	p.Config, p.Logs = os.Getenv(configVar), os.Getenv(logsVar)
	d := &dropper{}
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	go func() {
		for range usr1 {
			d.drop()
		}
	}()
	if err := run(ctx, options{Paths: p, Executable: exe, Version: version.Build(), wrapVMs: d.wrap}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// dropper passes connections through, and keeps the last one so that
// drop can close it.
type dropper struct {
	vms guestconn.VMs

	mu   sync.Mutex
	last net.Conn
}

func (d *dropper) wrap(vms guestconn.VMs) guestconn.VMs {
	d.vms = vms
	return d
}

func (d *dropper) ConnectGuest(ctx context.Context, slot vm.Slot, generation uint64, port uint32) (net.Conn, error) {
	c, err := d.vms.ConnectGuest(ctx, slot, generation, port)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.last = c
	d.mu.Unlock()
	return c, nil
}

func (d *dropper) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last != nil {
		_ = d.last.Close()
	}
}
