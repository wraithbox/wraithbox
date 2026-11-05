package fdchan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// The ordering test of S07-05-stream-hand-off, "Transport"
// (C11-host-daemons, "Descriptor channels"): a sender that retries a
// full channel in order and a slower receiver see every record once, in
// order, with no gap, and every descriptor works.

const (
	// orderRecords is how many records the ordering test sends.
	orderRecords = 100_000
	// orderFDEvery is how often a record carries a descriptor.
	orderFDEvery = 10
	// orderWriteDeadline is the sender's deadline for one record,
	// retries included: wb-proxyd's for a ProxydWildcardName record
	// (S07-06-name-mapping, "Wildcard budget, second layer").
	orderWriteDeadline = 100 * time.Millisecond
	// orderPauseEvery and orderPause make the receiver slower than the
	// sender, so the channel fills and the sender retries.
	orderPauseEvery = 1000
	orderPause      = 2 * time.Millisecond
	// orderHeader is the record's sequence number and its size.
	orderHeader = 8
)

// orderSizes are the record sizes the test cycles through: a release, a
// name report of a short and of a 253-byte name, and sizes up to the
// maximum.
var orderSizes = []int{orderHeader, 13, 40, 264, 700, 1500, 2900, testMax}

func orderSize(i int) int { return orderSizes[i%len(orderSizes)] }

// orderRecord fills rec with record i: its sequence number, its size,
// and bytes that depend on both.
func orderRecord(rec []byte, i int) []byte {
	rec = rec[:orderSize(i)]
	binary.BigEndian.PutUint32(rec, uint32(i))
	binary.BigEndian.PutUint32(rec[4:], uint32(len(rec)))
	for j := orderHeader; j < len(rec); j++ {
		rec[j] = byte(i + j)
	}
	return rec
}

// isFull reports whether a send failed because the peer's buffer is
// full, the one failure the sender retries.
func isFull(err error) bool {
	return errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.EAGAIN)
}

// orderSender sends the records in order, retrying a record whose send
// fails with a full buffer until it goes or its deadline passes, and
// never sending the next one before it. It returns how many records it
// sent and how many sends failed with a full buffer.
type orderSender struct {
	ch       *Channel
	sent     int
	failures int
}

func (s *orderSender) run(t *testing.T) error {
	rec := make([]byte, testMax)
	for i := range orderRecords {
		var fd *os.File
		if i%orderFDEvery == 0 {
			var err error
			if fd, err = orderPipe(i); err != nil {
				return err
			}
		}
		err := s.send(orderRecord(rec, i), fd)
		if fd != nil {
			_ = fd.Close()
		}
		if err != nil {
			t.Logf("record %d: %v", i, err)
			return err
		}
		s.sent++
	}
	return nil
}

func (s *orderSender) send(rec []byte, fd *os.File) error {
	deadline := time.Now().Add(orderWriteDeadline)
	if err := s.ch.SetWriteDeadline(deadline); err != nil {
		return err
	}
	for {
		var err error
		if fd == nil {
			err = s.ch.Send(rec, nil)
		} else {
			err = s.ch.Send(rec, fd)
		}
		switch {
		case err == nil:
			return nil
		case !isFull(err):
			return err
		case time.Now().After(deadline):
			return err
		}
		s.failures++
		time.Sleep(50 * time.Microsecond)
	}
}

// orderPipe returns the read end of a pipe that holds i and then end of
// file, the descriptor of record i.
func orderPipe(i int) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Write(binary.BigEndian.AppendUint64(nil, uint64(i))); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

// checkOrdered checks that m is record i: its sequence number, size and
// bytes, and a working descriptor exactly when record i has one.
func checkOrdered(m *Message, i int) error {
	want := orderRecord(make([]byte, testMax), i)
	switch {
	case len(m.Record) < orderHeader:
		return errors.New("a record shorter than its header")
	case binary.BigEndian.Uint32(m.Record) != uint32(i):
		return fmt.Errorf("out of order: sequence %d, want %d", binary.BigEndian.Uint32(m.Record), i)
	case !bytes.Equal(m.Record, want):
		return fmt.Errorf("record %d has other bytes or another size", i)
	case (m.File != nil) != (i%orderFDEvery == 0):
		return fmt.Errorf("record %d has the wrong descriptor count", i)
	case m.File == nil:
		return nil
	}
	return checkPipe(m.File, i)
}

// checkPipe reads a received pipe within a deadline, which the received
// descriptor must take, as a payload pipe's reader needs: i, then end
// of file.
func checkPipe(f *os.File, i int) error {
	if err := f.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	b, err := io.ReadAll(f)
	switch {
	case err != nil:
		return err
	case len(b) != 8 || binary.BigEndian.Uint64(b) != uint64(i):
		return fmt.Errorf("the descriptor of record %d holds another pipe", i)
	}
	return nil
}

func TestOrderUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("sends 100,000 records")
	}
	a, b := pair(t)
	s := &orderSender{ch: a}
	done := make(chan error, 1)
	go func() { done <- s.run(t) }()

	received := receiveOrdered(t, b)
	if err := <-done; err != nil {
		t.Fatalf("sender after %d records: %v", s.sent, err)
	}
	if err := b.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if m, err := b.Recv(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("after the last record: %v, %v, want nothing more", m, err)
	}
	if s.sent != orderRecords || received != orderRecords {
		t.Fatalf("sent %d, received %d, want %d each", s.sent, received, orderRecords)
	}
	if s.failures == 0 {
		t.Fatal("no send found the channel full: the receiver never fell behind, so the retry wasn't tested")
	}
	t.Logf("sent and received %d records in order, %d with a descriptor; %d sends found the channel full and were retried in order",
		orderRecords, orderRecords/orderFDEvery, s.failures)
}

// receiveOrdered receives orderRecords records, pausing now and then,
// and checks each is the next in order. It returns how many it received.
func receiveOrdered(t *testing.T, b *Channel) int {
	t.Helper()
	received := 0
	for i := range orderRecords {
		if i%orderPauseEvery == orderPauseEvery-1 {
			time.Sleep(orderPause)
		}
		if err := b.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		m, err := b.Recv()
		if err != nil {
			t.Fatalf("after %d records: %v", received, err)
		}
		err = checkOrdered(m, i)
		_ = m.Close()
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		received++
	}
	return received
}

// TestQueueDepth measures how many records a channel queues before a
// send with no retry fails with ENOBUFS or EAGAIN, for records of the
// maximum size and of a name report, and logs both
// (S07-05-stream-hand-off, "Transport").
func TestQueueDepth(t *testing.T) {
	for _, size := range []int{testMax, 264, orderHeader} {
		a, b := pair(t)
		n, err := fillSize(a, size)
		if !isFull(err) {
			t.Fatalf("records of %d bytes: after %d sends, err = %v, want ENOBUFS or EAGAIN", size, n, err)
		}
		if size == testMax && n < queuedRecords {
			t.Errorf("%d records of %d bytes queued, want at least queuedRecords, %d", n, size, queuedRecords)
		}
		t.Logf("queue depth: %d records of %d bytes queued before %v (queuedRecords=%d)", n, size, err, queuedRecords)
		if err := b.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		for i := range n {
			m, err := b.Recv()
			if err != nil || len(m.Record) != size || binary.BigEndian.Uint32(m.Record) != uint32(i) {
				t.Fatalf("records of %d bytes: record %d: %v", size, i, err)
			}
		}
	}
}

// fillSize sends numbered records of size bytes with no retry until a
// send fails, and returns how many it sent and the error.
func fillSize(c *Channel, size int) (int, error) {
	rec := make([]byte, size)
	for sent := range 100_000 {
		binary.BigEndian.PutUint32(rec, uint32(sent))
		if err := c.Send(rec, nil); err != nil {
			return sent, err
		}
	}
	return 100_000, nil
}
