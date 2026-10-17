package guestlink

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
)

// FuzzHelloResponse feeds arbitrary bytes through proto.Unmarshal into
// the checks Hello applies to wb-guestd's answer (C11-host-daemons,
// "Every vsock RPC handler in `wb-hostd`"). A refused answer never yields
// a version. An accepted one is 1 to 64 bytes of printable ASCII and the
// host's own version, and its package list is the host's own.
func FuzzHelloResponse(f *testing.F) {
	seed := func(v string) []byte {
		b, err := proto.Marshal(&guestv1.HelloResponse{GuestdVersion: v, ServedPackages: ownPackages()})
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	f.Add([]byte{})
	f.Add(seed(own))
	f.Add(seed("1.2.3-" + strings.Repeat("a", 58))) // 64 bytes
	f.Add(seed("1.2.3-" + strings.Repeat("a", 59))) // 65 bytes
	unknown := protowire.AppendTag(seed(own), 99, protowire.BytesType)
	f.Add(protowire.AppendBytes(unknown, []byte("unknown field")))

	f.Fuzz(func(t *testing.T, data []byte) {
		resp := &guestv1.HelloResponse{}
		if err := proto.Unmarshal(data, resp); err != nil {
			return // gRPC refuses an answer that doesn't decode (RuleInvalid)
		}
		v, r := check(own, resp)
		if r != nil {
			requireRefusal(t, v, r)
			return
		}
		requireAccepted(t, v, resp)
	})
}

// requireRefusal: a refusal names its rule and yields no version.
func requireRefusal(t *testing.T, v string, r *Refusal) {
	t.Helper()
	if v != "" {
		t.Fatalf("refused (%s) but yielded version %q", r.Rule, v)
	}
	if r.Rule == "" {
		t.Fatal("refusal without a rule")
	}
}

// requireAccepted: an accepted answer has a version of 1 to 64 printable
// ASCII bytes that is the host's own, and the host's package list.
func requireAccepted(t *testing.T, v string, resp *guestv1.HelloResponse) {
	t.Helper()
	if len(v) < 1 || len(v) > 64 {
		t.Fatalf("accepted version of %d bytes", len(v))
	}
	for _, b := range []byte(v) {
		if b < 0x20 || b > 0x7e {
			t.Fatalf("accepted version %q has byte %#x", v, b)
		}
	}
	if v != own {
		t.Fatalf("accepted version %q, want only the host's %q", v, own)
	}
	if !slices.Equal(resp.GetServedPackages(), ownPackages()) {
		t.Fatalf("accepted packages %q", resp.GetServedPackages())
	}
}
