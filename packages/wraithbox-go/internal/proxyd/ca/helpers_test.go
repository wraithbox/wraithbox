package ca

import (
	"bytes"
	"context"
	"crypto/x509"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey/hwkeytest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

const (
	team = "ABCDE12345"
	caA  = "0123456789abcdef0123456789abcdef"
	caB  = "fedcba9876543210fedcba9876543210"
	caC  = "00000000000000000000000000000001"
)

// start is the fake clock's first reading.
var start = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// lockedBuffer is a log sink for concurrent writers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// hookedBackend is the fake store with a hook that runs after each
// write that succeeded, so a test can fail a later call of one issue.
type hookedBackend struct {
	*storetest.Fake
	afterWrite func()
}

func (h *hookedBackend) Write(ctx context.Context, group, item string, value []byte) secretstore.Status {
	st := h.Fake.Write(ctx, group, item, value)
	if st == secretstore.StatusOK && h.afterWrite != nil {
		h.afterWrite()
	}
	return st
}

// env is an Authority over the fake store and the fake key store, on a
// fake clock.
type env struct {
	t       testing.TB
	backend *storetest.Fake
	hooked  *hookedBackend
	keys    *hwkeytest.Fake
	logs    *lockedBuffer
	a       *Authority

	mu  sync.Mutex
	now time.Time
}

// newEnv returns an env whose store claims team, "" for an ad-hoc
// signed build.
func newEnv(t testing.TB, team string) *env {
	t.Helper()
	e := &env{
		t:       t,
		backend: storetest.New(team, storeitem.Group(team, storeitem.RoleCA)),
		keys:    &hwkeytest.Fake{},
		logs:    &lockedBuffer{},
		now:     start,
	}
	e.hooked = &hookedBackend{Fake: e.backend}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store, err := secretstore.New(storeitem.ProgramProxyd, e.hooked, log)
	if team != "" && err != nil {
		t.Fatalf("secretstore.New: %v", err)
	}
	e.a = New(Config{Store: store, Keys: e.keys, Clock: e.clock, Log: log, UnlockPoll: time.Millisecond})
	return e
}

// restart replaces the Authority with a new one over the same store and
// key store, as a new wb-proxyd process.
func (e *env) restart() {
	a := e.a
	e.a = New(Config{Store: a.store, Keys: a.keys, Clock: e.clock, Log: a.log, UnlockPoll: a.unlockPoll})
}

// waitLog waits until the logs hold s.
func (e *env) waitLog(s string) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(e.logs.String(), s) {
		if time.Now().After(deadline) {
			e.t.Fatalf("the logs never held %q:\n%s", s, e.logs)
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// advance moves the fake clock on by d.
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

// item is the full name of a VM's CA item.
func item(vm, id string) string { return storeitem.Prefix + "ca/" + vm + "/" + id }

// issue issues a CA and fails the test unless it succeeds.
func (e *env) issue(vm, id string) []byte {
	e.t.Helper()
	got := e.a.Issue(e.t.Context(), &egressv1.CaIssue{Vm: vm, CaId: id})
	if got.GetRule() != "" || len(got.GetCertificate()) == 0 {
		e.t.Fatalf("Issue(%s, %s) = rule %q, %d bytes; logs:\n%s", vm, id, got.GetRule(), len(got.GetCertificate()), e.logs)
	}
	return got.GetCertificate()
}

// set sends a CaSet whose CAs are given as id, certificate pairs, and
// returns the reply.
func (e *env) set(vm string, gen uint64, signing string, cas ...any) *egressv1.ProxydToHostd {
	e.t.Helper()
	m := &egressv1.CaSet{Vm: vm, Generation: gen, SigningCaId: signing}
	if signing == "" {
		m.NoSigningRule = RuleNotInstalled
	}
	for i := 0; i < len(cas); i += 2 {
		id, okID := cas[i].(string)
		der, okDER := cas[i+1].([]byte)
		if !okID || !okDER {
			e.t.Fatalf("set takes id, certificate pairs, not %T, %T", cas[i], cas[i+1])
		}
		m.Cas = append(m.Cas, &egressv1.InspectionCa{CaId: id, Certificate: der})
	}
	reply, err := e.a.SetCAs(e.t.Context(), m)
	if err != nil {
		e.t.Fatalf("SetCAs: %v", err)
	}
	return reply
}

// loaded sends a CaSet and fails the test unless it loaded.
func (e *env) loaded(vm string, gen uint64, signing string, cas ...any) {
	e.t.Helper()
	if reply := e.set(vm, gen, signing, cas...); reply.GetCaLoaded() == nil {
		e.t.Fatalf("SetCAs = %v, want CaLoaded; logs:\n%s", reply, e.logs)
	}
}

// accept sends a CaAccept and fails the test on an error.
func (e *env) accept(vm string, gen uint64) {
	e.t.Helper()
	if err := e.a.Accept(&egressv1.CaAccept{Vm: vm, Generation: gen}); err != nil {
		e.t.Fatalf("Accept: %v", err)
	}
}

// ready issues CA caA for VM work, loads it as the signing CA of
// generation 1 and accepts it, and returns its certificate.
func (e *env) ready() *x509.Certificate {
	const vm = "work"
	e.t.Helper()
	der := e.issue(vm, caA)
	e.loaded(vm, 1, caA, caA, der)
	e.accept(vm, 1)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		e.t.Fatal(err)
	}
	return cert
}

// leafPort is the port of every leafReq.
const leafPort = 443

// leafReq is a leaf request for name on leafPort, with a policy that
// inspects name there. A name that isn't a host name gets no policy.
func leafReq(vm string, gen uint64, name string) LeafRequest {
	req := LeafRequest{VM: vm, Generation: gen, Name: name, Port: leafPort}
	if n, ref := dnsname.FromHost(name); ref == nil {
		req.Policy = &policy.Policy{Endpoints: []policy.Endpoint{{Host: match.ExactHost(n), Ports: []uint16{leafPort}}}}
	}
	return req
}

// wantRefusal fails the test unless For refuses with rule, and with
// reason when it isn't "".
func (e *env) wantRefusal(vm string, gen uint64, name, rule, reason string) {
	e.t.Helper()
	leaf, ref := e.a.For(leafReq(vm, gen, name))
	if leaf != nil || ref == nil || ref.Rule != rule || (reason != "" && !strings.HasPrefix(ref.Reason, reason)) {
		e.t.Fatalf("For(%s, %d, %q) = %v, %v, want refusal %s: %s", vm, gen, name, leaf != nil, ref, rule, reason)
	}
}

// has reports whether the fake store holds item.
func (e *env) has(name string) bool {
	e.t.Helper()
	n, err := storeitem.Parse(name)
	if err != nil {
		e.t.Fatal(err)
	}
	_, err = e.a.store.Fetch(e.t.Context(), n, storeitem.RoleCA)
	return err == nil
}

// write puts value in the fake store as item.
func (e *env) write(name string, value []byte) {
	e.t.Helper()
	n, err := storeitem.Parse(name)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.a.store.Write(e.t.Context(), n, storeitem.RoleCA, value); err != nil {
		e.t.Fatal(err)
	}
}
