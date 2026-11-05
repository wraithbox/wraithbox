// Package hostlink is wb-netd's side of its two channels to wb-hostd:
// the accounting channel (S04-08-records-and-bounds, "Accounting
// records") and the control channel (control.proto, S04-07-supervision).
package hostlink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"syscall"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostversion"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/fdchan"
)

// ErrChannelEnded is the error of a write after the accounting channel
// ended.
var ErrChannelEnded = errors.New("hostlink: the accounting channel ended")

// Conn is a channel to wb-hostd: an *fdchan.Channel, or a fake in
// tests.
type Conn interface {
	Send(record []byte, fd syscall.Conn) error
	Recv() (*fdchan.Message, error)
	Close() error
}

// Accounting writes wb-netd's accounting records to wb-hostd
// (netd.Accounting): a WildcardDebit for each debit, a MappingEntry for
// each new or renewed entry, and a MappingRelease for each release. A
// write is bounded by its caller's context, 100 ms in the pool and the
// DNS server, and one that fails or returns after it failed. Nothing
// rate-limits or drops a record. It is safe for concurrent use.
type Accounting struct {
	conn Conn
	own  string
	rec  *netd.Recorder
	log  *slog.Logger

	lock  chan struct{} // held across each write, so records keep their order
	ready chan struct{} // closed when wb-hostd's version record passed
	done  chan struct{} // closed when the channel ended

	once sync.Once
	mu   sync.Mutex
	err  error // why the channel ended
}

// NewAccounting sends the version record on conn and starts reading
// wb-hostd's version record from it. Writes wait for that record, within
// their deadline. log nil is slog.Default.
func NewAccounting(conn Conn, own string, log *slog.Logger) (*Accounting, error) {
	if log == nil {
		log = slog.Default()
	}
	a := &Accounting{
		conn: conn, own: own, log: log, rec: netd.NewRecorder(log, nil),
		lock: make(chan struct{}, 1), ready: make(chan struct{}), done: make(chan struct{}),
	}
	if err := conn.Send(hostversion.Record(own), nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("hostlink: send the version record: %w", err)
	}
	go a.read()
	return a, nil
}

// Recorder returns the writer's recorder, for its counts.
func (a *Accounting) Recorder() *netd.Recorder { return a.rec }

// Entry writes a MappingEntry record.
func (a *Accounting) Entry(ctx context.Context, name dnsname.Name, addr netip.Addr) error {
	wire, ok := egressmsg.AddrToWire(addr)
	if !ok {
		return fmt.Errorf("hostlink: %v isn't an IPv4 address", addr)
	}
	return a.write(ctx, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingEntry{
		MappingEntry: &egressv1.MappingEntry{Name: name.String(), Address: wire},
	}})
}

// Release writes a MappingRelease record.
func (a *Accounting) Release(ctx context.Context, addr netip.Addr) error {
	wire, ok := egressmsg.AddrToWire(addr)
	if !ok {
		return fmt.Errorf("hostlink: %v isn't an IPv4 address", addr)
	}
	return a.write(ctx, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingRelease{
		MappingRelease: &egressv1.MappingRelease{Address: wire},
	}})
}

// Debit writes a WildcardDebit record.
func (a *Accounting) Debit(ctx context.Context, name dnsname.Name) error {
	return a.write(ctx, &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_WildcardDebit{
		WildcardDebit: &egressv1.WildcardDebit{Name: name.String()},
	}})
}

// Close closes the channel. Later writes fail.
func (a *Accounting) Close() error {
	a.end(errors.New("closed"))
	return nil
}

// write sends one record under the lock, once wb-hostd's version record
// passed, all within ctx. A write that returns after ctx's deadline
// failed, even when the channel took it.
func (a *Accounting) write(ctx context.Context, m proto.Message) error {
	rec, err := egressmsg.Marshal(m)
	if err != nil {
		return fmt.Errorf("hostlink: %w", err)
	}
	select {
	case a.lock <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("hostlink: wait for the write lock: %w", context.Cause(ctx))
	}
	defer func() { <-a.lock }()
	select {
	case <-a.ready:
	case <-a.done:
		return fmt.Errorf("%w: %w", ErrChannelEnded, a.cause())
	case <-ctx.Done():
		return fmt.Errorf("hostlink: wait for wb-hostd's version record: %w", context.Cause(ctx))
	}
	if err := a.conn.Send(rec, nil); err != nil {
		return fmt.Errorf("hostlink: write: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hostlink: write: %w", context.Cause(ctx))
	}
	return nil
}

// read checks wb-hostd's version record, then reads until the channel
// ends. wb-hostd sends nothing after its version record, so a record
// after it is logged and dropped.
func (a *Accounting) read() {
	first := true
	for {
		m, err := a.conn.Recv()
		var fref *fdchan.Refusal
		switch {
		case errors.Is(err, io.EOF):
			a.end(errors.New("wb-hostd closed the accounting channel"))
			return
		case errors.As(err, &fref) && !first:
			// fdchan logged it and closed its descriptors.
			continue
		case err != nil:
			a.end(fmt.Errorf("read: %w", err))
			return
		}
		hasFD := m.File != nil
		_ = m.Close()
		if !first {
			a.rec.Note(netd.RuleControlMessageRefused, slog.String("channel", "accounting"),
				slog.String("decision", "drop"), slog.Int("bytes", len(m.Record)), slog.Bool("descriptor", hasFD))
			continue
		}
		first = false
		if err := a.version(m.Record, hasFD); err != nil {
			a.end(err)
			return
		}
		close(a.ready)
	}
}

// version checks wb-hostd's version record.
func (a *Accounting) version(rec []byte, hasFD bool) error {
	if hasFD {
		a.rec.Note(netd.RuleControlMessageRefused, slog.String("channel", "accounting"),
			slog.String("check", "version-record"), slog.String("decision", "close"))
		return errors.New("the version record carries a descriptor")
	}
	_, err := hostversion.CheckRecord(a.log, a.own, rec)
	return err
}

func (a *Accounting) cause() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// end closes the channel. Only the first call does anything.
func (a *Accounting) end(why error) {
	a.once.Do(func() {
		a.mu.Lock()
		a.err = why
		a.mu.Unlock()
		close(a.done)
		_ = a.conn.Close()
		a.log.LogAttrs(context.Background(), slog.LevelWarn, "netd accounting channel ended",
			slog.String("reason", why.Error()))
	})
}
