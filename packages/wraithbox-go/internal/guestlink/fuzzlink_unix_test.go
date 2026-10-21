//go:build unix

package guestlink

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// fuzzDeadline is the deadline of each Hello in FuzzGuestLink, shorter
// than HelloTimeout so the fuzzer runs many inputs.
const fuzzDeadline = 2 * time.Second

// FuzzGuestLink writes arbitrary bytes, in arbitrary chunks, from the
// guest end of a socketpair as the answer to the link's open: the
// HTTP/2 preface, settings and the Hello (C11-host-daemons, "Every
// vsock RPC handler in `wb-hostd`"). The link ends linked or refused
// within its deadline, never panics, and never accepts a version other
// than its own. cuts gives the chunk lengths, one byte each.
func FuzzGuestLink(f *testing.F) {
	valid := guestAnswer(f, own)
	// A cut of 8 makes the first chunk the 9-byte SETTINGS frame.
	f.Add(valid, []byte{8})
	f.Add(valid, []byte{8, 0, 1, 2, 4, 7, 12})
	f.Add(valid[:len(valid)/2], []byte{8})
	f.Add(guestAnswer(f, "1.2.4"), []byte{8})
	f.Add(guestAnswer(f, "1.2.3\x1b]0;x\x07"), []byte{8, 40})
	f.Add([]byte{}, []byte{})
	f.Add([]byte("HTTP/1.1 200 OK\r\n\r\n"), []byte{})

	f.Fuzz(func(t *testing.T, data, cuts []byte) {
		host, guest := socketpair(t)
		go playGuest(guest, data, cuts)
		ctx, cancel := context.WithTimeout(context.Background(), fuzzDeadline)
		defer cancel()
		start := time.Now()
		l, err := Hello(ctx, host, own, discard)
		if d := time.Since(start); d > fuzzDeadline+time.Second {
			t.Fatalf("Hello took %v, past its deadline of %v", d, fuzzDeadline)
		}
		if err != nil {
			if _, ok := errors.AsType[*Refusal](err); !ok {
				t.Fatalf("Hello failed with %v (%T), want a *Refusal", err, err)
			}
			return
		}
		defer l.Close()
		if l.Version() != own {
			t.Fatalf("linked with version %q, want only the host's %q", l.Version(), own)
		}
	})
}

// playGuest reads the host's preface, writes data's first chunk, waits
// until the host has opened its stream (or a moment), writes the rest,
// then closes. It drains what the host sends all along, so the host
// never blocks on a write.
func playGuest(guest net.Conn, data, cuts []byte) {
	defer guest.Close()
	opened := make(chan struct{})
	go readHost(guest, opened)
	chunks := split(data, cuts)
	for i, c := range chunks {
		if i == 1 {
			select {
			case <-opened:
			case <-time.After(100 * time.Millisecond):
			}
		}
		if _, err := guest.Write(c); err != nil {
			return
		}
	}
}

// readHost reads the host's client preface and frames, and closes
// opened at the first HEADERS frame: the host's Hello request.
func readHost(guest net.Conn, opened chan<- struct{}) {
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(guest, preface); err != nil {
		close(opened)
		return
	}
	fr := http2.NewFramer(io.Discard, guest)
	signaled := false
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			if !signaled {
				close(opened)
			}
			_, _ = io.Copy(io.Discard, guest)
			return
		}
		if _, ok := frame.(*http2.HeadersFrame); ok && !signaled {
			signaled = true
			close(opened)
		}
	}
}

// split cuts data into chunks of the lengths in cuts, 1 to 256 bytes
// each, and the rest as the last chunk.
func split(data, cuts []byte) [][]byte {
	var out [][]byte
	for _, c := range cuts {
		if len(data) == 0 {
			break
		}
		n := min(int(c)+1, len(data))
		out = append(out, data[:n])
		data = data[n:]
	}
	if len(data) > 0 {
		out = append(out, data)
	}
	return out
}

// guestAnswer is what a gRPC server writes for a Hello that answers v:
// its settings, the ack of the host's, and the answer on stream 1. The
// first frame is the 9-byte empty SETTINGS frame.
func guestAnswer(tb testing.TB, v string) []byte {
	tb.Helper()
	msg, err := proto.Marshal(&guestv1.HelloResponse{GuestdVersion: v, ServedPackages: ownPackages()})
	if err != nil {
		tb.Fatal(err)
	}
	var buf bytes.Buffer
	fr := http2.NewFramer(&buf, nil)
	block := func(fields ...hpack.HeaderField) []byte {
		var b bytes.Buffer
		enc := hpack.NewEncoder(&b)
		for _, hf := range fields {
			if err := enc.WriteField(hf); err != nil {
				tb.Fatal(err)
			}
		}
		return b.Bytes()
	}
	data := binary.BigEndian.AppendUint32([]byte{0}, uint32(len(msg)))
	errs := []error{
		fr.WriteSettings(),
		fr.WriteSettingsAck(),
		fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: block(
			hpack.HeaderField{Name: ":status", Value: "200"},
			hpack.HeaderField{Name: "content-type", Value: "application/grpc"},
		)}),
		fr.WriteData(1, false, append(data, msg...)),
		fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, EndStream: true, BlockFragment: block(
			hpack.HeaderField{Name: "grpc-status", Value: "0"},
		)}),
	}
	if err := errors.Join(errs...); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// TestGuestAnswerLinks: the valid seed of FuzzGuestLink links, so the
// fuzzer starts from an exchange that passes, and the other-version
// seed is refused.
func TestGuestAnswerLinks(t *testing.T) {
	host, guest := socketpair(t)
	go playGuest(guest, guestAnswer(t, own), []byte{8})
	l, err := Hello(t.Context(), host, own, discard)
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	_ = l.Close()

	host, guest = socketpair(t)
	go playGuest(guest, guestAnswer(t, "1.2.4"), []byte{8})
	if _, err := Hello(t.Context(), host, own, discard); err == nil {
		t.Fatal("another version linked")
	}
}
