package ca

import (
	"crypto"
	"crypto/tls"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// TestC02LeafNotInspected: a host and port the VM's policy doesn't
// inspect gets no leaf: a pass host, plain HTTP, a raw TCP entry, a port
// no endpoint names, and no policy (S09-07-ca-certificates, "Leaf
// issuance"). Each is refused with ca-leaf-refused, logged, and nothing
// is signed.
func TestC02LeafNotInspected(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	host, ref := dnsname.FromHost("example.com")
	if ref != nil {
		t.Fatal(ref)
	}
	src := policy.Source{Kind: policy.SourceGlobal}
	p := &policy.Policy{
		Endpoints: []policy.Endpoint{{Host: match.ExactHost(host), Ports: []uint16{80, 443}}},
		PassHosts: []policy.PassHost{{Host: host, Source: src, Reason: "pinned"}},
		RawTCP:    []policy.RawTCP{{Host: host, Port: 22, Source: src, Reason: "ssh"}},
	}
	for _, c := range []struct {
		what string
		p    *policy.Policy
		port uint16
	}{
		{"a pass host", p, 443},
		{"plain HTTP", p, 80},
		{"a raw TCP entry", p, 22},
		{"a port no endpoint names", p, 8443},
		{"no policy", nil, 443},
	} {
		req := LeafRequest{VM: "work", Generation: 1, Name: "example.com", Port: c.port, Policy: c.p}
		leaf, ref := e.a.For(req)
		if leaf != nil || ref == nil || ref.Rule != RuleLeafRefused || ref.Reason != ReasonNotInspected {
			t.Errorf("%s: For = %v, %v, want %s: %s", c.what, leaf != nil, ref, RuleLeafRefused, ReasonNotInspected)
		}
	}
	logs := e.logs.String()
	if strings.Contains(logs, `msg="leaf signed"`) {
		t.Errorf("a leaf was signed:\n%s", logs)
	}
	want := `rule=ca-leaf-refused reason="the VM's policy doesn't inspect the host and port" decision=refuse port=443`
	if !strings.Contains(logs, want) {
		t.Errorf("the refusal isn't logged with its rule:\n%s", logs)
	}
}

// gatedKey is a CA key whose signatures wait for release, and are
// counted.
type gatedKey struct {
	hwkey.Key
	entered chan struct{}
	release chan struct{}
	signs   atomic.Int32
}

func (k *gatedKey) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if k.signs.Add(1) == 1 {
		close(k.entered)
	}
	<-k.release
	return k.Key.Sign(r, digest, opts)
}

// TestLeafSingleflight: parallel calls for one new hostname wait for the
// first call's signature, so the leaf is signed once and each call gets
// it.
func TestLeafSingleflight(t *testing.T) {
	e := newEnv(t, team)
	e.ready()
	held := e.a.vms["work"].cas[caA].key
	gate := &gatedKey{Key: held.key, entered: make(chan struct{}), release: make(chan struct{})}
	held.mu.Lock()
	held.key = gate
	held.mu.Unlock()

	const calls = 8
	leaves := make(chan *tls.Certificate, calls)
	ask := func() {
		leaf, ref := e.a.For(leafReq("work", 1, "new.example.com"))
		if ref != nil {
			t.Error(ref)
		}
		leaves <- leaf
	}
	go ask()
	<-gate.entered
	for range calls - 1 {
		go ask()
	}
	// Let the other calls reach the flight, which the first call holds
	// while its signature waits.
	time.Sleep(20 * time.Millisecond)
	close(gate.release)
	first := <-leaves
	for range calls - 1 {
		if leaf := <-leaves; leaf != first {
			t.Error("a parallel call got another leaf")
		}
	}
	if n := gate.signs.Load(); n != 1 {
		t.Errorf("the leaf was signed %d times, want once", n)
	}
	e.a.mu.Lock()
	n := len(e.a.vms["work"].leaves.flights)
	e.a.mu.Unlock()
	if n != 0 {
		t.Errorf("%d flights left after the signature", n)
	}
}

// brokenKey is a CA key whose first signature fails, or panics with
// panics set, and whose later signatures succeed.
type brokenKey struct {
	hwkey.Key
	panics bool
	signs  atomic.Int32
}

func (k *brokenKey) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if k.signs.Add(1) > 1 {
		return k.Key.Sign(r, digest, opts)
	}
	if k.panics {
		panic("the key broke")
	}
	return nil, errors.New("the key refused")
}

// TestLeafFlightFails: a first signature that fails, or panics, ends its
// flight, with a refusal when it fails, and the next call signs the
// leaf.
func TestLeafFlightFails(t *testing.T) {
	for _, panics := range []bool{false, true} {
		e := newEnv(t, team)
		e.ready()
		held := e.a.vms["work"].cas[caA].key
		key := &brokenKey{Key: held.key, panics: panics}
		held.mu.Lock()
		held.key = key
		held.mu.Unlock()

		leaf, ref := forRecovered(e.a, leafReq("work", 1, "new.example.com"))
		if leaf != nil || (!panics && (ref == nil || ref.Rule != RuleLeafRefused)) {
			t.Errorf("panics=%v: first For = %v, %v, want a refusal", panics, leaf != nil, ref)
		}
		e.a.mu.Lock()
		n := len(e.a.vms["work"].leaves.flights)
		e.a.mu.Unlock()
		if n != 0 {
			t.Errorf("panics=%v: %d flights left after the failed signature", panics, n)
		}
		if leaf, ref := e.a.For(leafReq("work", 1, "new.example.com")); leaf == nil || ref != nil {
			t.Errorf("panics=%v: next For = %v, %v, want a leaf", panics, leaf != nil, ref)
		}
	}
}

// forRecovered calls For, and returns no leaf and no refusal when it
// panics.
func forRecovered(a *Authority, req LeafRequest) (leaf *tls.Certificate, ref *Refusal) {
	defer func() { _ = recover() }()
	return a.For(req)
}
