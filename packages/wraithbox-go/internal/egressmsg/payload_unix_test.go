//go:build unix

package egressmsg

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// The payload pipe tests need a pipe that takes a read deadline, which
// os.Pipe gives on darwin and linux. Payloads cross an fdchan channel,
// which only those have.

func TestReadPayloadPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := &egressv1.ProxydStartPayload{WildcardNames: []*egressv1.WildcardNames{{Vm: "work", Names: []string{"a.example"}}}}
	go func() {
		_ = WritePayload(w, want)
		_ = w.Close()
	}()
	got := &egressv1.ProxydStartPayload{}
	if ref := ReadPayload(r, got); ref != nil {
		t.Fatal(ref)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
}

// A writer that never closes its end is refused at the deadline, not
// waited for.
func TestReadPayloadDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if err := WritePayload(w, &egressv1.NetdStartPayload{DebitCount: 1}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	wantCheck(t, readPipe(r, &egressv1.NetdStartPayload{}, time.Now().Add(50*time.Millisecond)), CheckPayload)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("refused after %v", d)
	}
}

func TestReadPayloadNotAPipe(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := WritePayload(f, &egressv1.NetdStartPayload{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, ReadPayload(f, &egressv1.NetdStartPayload{}), CheckPayload)
}
