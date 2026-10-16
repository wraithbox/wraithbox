// Package logpipe writes the daemons' logs to <logs>, one file per
// process (S04-08-records-and-bounds, "Logs and health").
//
// A daemon writes its log to a pipe that wb-hostd handed it at spawn. It
// holds no descriptor on a file in <logs> (S04-03-self-sandboxing,
// "Daemon logs", SEC12-least-privilege). Copy reads that pipe, and
// writes each line as one JSON record of wb-hostd's, with the time it
// was received, the process and the line. The process name comes from
// the spawn, never from the bytes. So no byte in a line can forge
// another process's record or a second record.
//
// The bounds are the "Daemon log lines" row of S04-08-records-and-bounds,
// "Host work the guest can cause" (SEC13-bounded-resources). Daemon logs
// can carry bytes the guest influenced, so every byte is treated as
// adversarial:
//
//   - at most 100 lines a second per process, and one record with the
//     count of the lines dropped over that rate;
//   - a line cut at 4 KiB, with a truncation marker, and the rest of it
//     up to the next newline discarded;
//   - a reader that holds at most one line and a fixed read buffer, and
//     keeps draining the pipe while a file write is slow. A line that
//     finds the queue full is dropped and counted, so a daemon never
//     blocks on its log;
//   - a file rotated at 10 MiB, with the last 5 files kept (File).
//
// wb-hostd's own log uses File as well, in <logs>/wb-hostd.log.
package logpipe

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// The bounds of S04-08-records-and-bounds, "Logs and health".
const (
	// MaxLine is where a line is cut, in bytes.
	MaxLine = 4096
	// LinesPerSecond is the rate of lines written per process.
	LinesPerSecond = 100
	// readSize is the fixed read buffer.
	readSize = 4096
	// queueLen bounds the lines waiting for a slow write: a second of
	// lines at the full rate, and some room for drop records.
	queueLen = 128
)

// The rules of the drop records.
const (
	// RuleRate: lines dropped over LinesPerSecond.
	RuleRate = "daemon-log-rate"
	// RuleBacklog: lines dropped because the queue was full while a
	// write was slow, or because a write failed.
	RuleBacklog = "daemon-log-backlog"
)

// errProcessName refuses a process name outside the fixed form.
var errProcessName = errors.New("logpipe: a process name is 1 to 64 lowercase letters, digits and hyphens, starting with a letter")

// checkProcess checks the process name a caller gives. It names a file
// in <logs> and is written into each record unescaped, so it has a fixed
// form.
func checkProcess(name string) error {
	if name == "" || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return errProcessName
	}
	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return errProcessName
		}
	}
	return nil
}

// entry is one record waiting to be written: a line, or a drop count.
type entry struct {
	at      time.Time
	line    []byte
	cut     bool
	rule    string
	dropped int64
}

// Copy reads process's log stream from r until EOF, and writes one
// record per line to w, one Write call per record. It returns when r is
// drained and every queued record is written or counted as dropped. A
// read error other than EOF ends the stream and is returned. A write
// error drops the record and is counted (RuleBacklog).
func Copy(r io.Reader, w io.Writer, process string) error {
	return copyWith(r, w, process, time.Now)
}

func copyWith(r io.Reader, w io.Writer, process string, now func() time.Time) error {
	if err := checkProcess(process); err != nil {
		return err
	}
	p := &pipe{
		process: process,
		now:     now,
		queue:   make(chan entry, queueLen),
		split:   newSplitter(),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.write(w)
	}()
	err := p.read(r)
	<-done
	return err
}

type pipe struct {
	process string
	now     func() time.Time
	queue   chan entry
	split   splitter

	// The rate window, owned by the reader.
	windowStart time.Time
	inWindow    int
	overRate    int64

	// backlog counts the lines dropped by the reader on a full queue and
	// the records the writer failed to write.
	backlog atomic.Int64
}

// read drains r. It never blocks on the writer, apart from the final
// rate record after EOF.
func (p *pipe) read(r io.Reader) error {
	buf := make([]byte, readSize)
	var err error
	for {
		var n int
		n, err = r.Read(buf)
		p.split.feed(buf[:n], p.offer)
		if err != nil {
			break
		}
	}
	p.split.end(p.offer)
	if p.overRate > 0 {
		p.queue <- p.rateRecord(p.now())
	}
	close(p.queue)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("logpipe: reading the log of %s: %w", p.process, err)
}

// offer takes one line from the splitter. line is reused after offer
// returns, so a queued line is a copy.
func (p *pipe) offer(line []byte, cut bool) {
	now := p.now()
	if p.windowStart.IsZero() || now.Sub(p.windowStart) >= time.Second || now.Before(p.windowStart) {
		p.turnWindow(now)
	}
	if p.inWindow >= LinesPerSecond {
		p.overRate++
		return
	}
	p.inWindow++
	p.enqueue(entry{at: now, line: append([]byte(nil), line...), cut: cut})
}

// turnWindow starts a new one-second window. The count of lines dropped
// over the rate goes out as one record when the window turns. If the
// queue is full, the count stays and goes out at a later turn, so it is
// never lost.
func (p *pipe) turnWindow(now time.Time) {
	if p.overRate > 0 {
		select {
		case p.queue <- p.rateRecord(now):
			p.overRate = 0
		default:
		}
	}
	p.windowStart = now
	p.inWindow = 0
}

func (p *pipe) rateRecord(now time.Time) entry {
	return entry{at: now, rule: RuleRate, dropped: p.overRate}
}

func (p *pipe) enqueue(e entry) {
	select {
	case p.queue <- e:
	default:
		p.backlog.Add(1)
	}
}

// write writes queued records until the queue closes. Each time it has
// caught up, it writes the backlog count as one record.
func (p *pipe) write(w io.Writer) {
	var buf []byte
	for e := range p.queue {
		buf = appendRecord(buf[:0], p.process, e)
		if _, err := w.Write(buf); err != nil {
			p.backlog.Add(1)
		}
		if len(p.queue) == 0 {
			buf = p.writeBacklog(w, buf)
		}
	}
	p.writeBacklog(w, buf)
}

func (p *pipe) writeBacklog(w io.Writer, buf []byte) []byte {
	n := p.backlog.Swap(0)
	if n == 0 {
		return buf
	}
	buf = appendRecord(buf[:0], p.process, entry{at: p.now(), rule: RuleBacklog, dropped: n})
	if _, err := w.Write(buf); err != nil {
		p.backlog.Add(n)
	}
	return buf
}
