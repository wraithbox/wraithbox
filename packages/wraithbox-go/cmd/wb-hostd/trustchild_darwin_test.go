package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"google.golang.org/protobuf/proto"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/egressmsg"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestlink"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/carecord"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/guestconn"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/vm"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey/hwkeytest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore/storetest"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/proxyd/ca"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// trustVar names the directory where the child's fake wb-proxyd end
// writes the leaf of each CA set it accepts. Only TestC02GuestTrustVM
// sets it, and then the child's CA record has that fake end and runs
// its boot order on each boot time call (options.caTest).
const trustVar = "WB_HOSTD_TEST_TRUST"

// leafHost is the name of the leaves the fake signs. A name under
// .invalid never resolves (RFC 6761), so each client in the guest is
// told the server's address.
const leafHost = "wbtest.invalid"

// fakeTeam is the team ID the fake secret store claims.
const fakeTeam = "ABCDE12345"

// The log messages of the fake, which the test looks for in the
// child's output, child.out.
const (
	msgFakeEnded  = "fake wb-proxyd ended"
	msgBootFailed = "test CA boot order failed"
)

// leafPath is the PEM file of the leaf the fake signed for generation:
// its chain, then its PKCS8 key.
func leafPath(dir string, generation uint64) string {
	return filepath.Join(dir, "leaf-"+strconv.FormatUint(generation, 10)+".pem")
}

// fakeProxyd is wb-proxyd's end of the control channel, for the CA
// messages only: wb-proxyd's CA authority over the fake secret store
// and the software fake key store, so no Keychain item is made. Each
// message goes through wb-proxyd's decoder first, as on the real
// channel.
type fakeProxyd struct {
	authority *ca.Authority
	dir       string
	log       *slog.Logger
}

// newFakeCA returns the child's caTest: a fake wb-proxyd end that
// writes the leaf of each accepted CA set to dir.
func newFakeCA(dir string, log *slog.Logger) (*caTest, error) {
	backend := storetest.New(storeitem.ProgramProxyd, fakeTeam, storeitem.Group(fakeTeam, storeitem.RoleCA))
	store, err := secretstore.New(storeitem.ProgramProxyd, backend, log)
	if err != nil {
		return nil, err
	}
	p := &fakeProxyd{
		authority: ca.New(ca.Config{Store: store, Keys: &hwkeytest.Fake{}, Log: log}),
		dir:       dir, log: log,
	}
	return &caTest{Proxyd: p, Ender: p, Boot: p.boot}, nil
}

// decode sends m through wb-proxyd's decoder of the control channel.
func decode(m *egressv1.HostdToProxyd) (*egressv1.HostdToProxyd, error) {
	rec, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	got, ref := egressmsg.DecodeHostdToProxyd(rec, false)
	if ref != nil {
		return nil, ref
	}
	return got, nil
}

func (p *fakeProxyd) Call(ctx context.Context, m *egressv1.HostdToProxyd) ([]byte, error) {
	m, err := decode(m)
	if err != nil {
		return nil, err
	}
	reply, err := p.authority.Handle(ctx, m)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, fmt.Errorf("fake wb-proxyd: no reply to %T", m.GetKind())
	}
	return proto.Marshal(reply)
}

// Send takes a CaAccept, and then writes the leaf of leafHost that the
// accepted CA set signs.
func (p *fakeProxyd) Send(ctx context.Context, m *egressv1.HostdToProxyd) error {
	m, err := decode(m)
	if err != nil {
		return err
	}
	accept := m.GetCaAccept()
	if accept == nil {
		return fmt.Errorf("fake wb-proxyd: Send of %T", m.GetKind())
	}
	if _, err := p.authority.Handle(ctx, m); err != nil {
		return err
	}
	return p.writeLeaf(accept.GetVm(), accept.GetGeneration())
}

// writeLeaf signs a leaf of leafHost for vm's run of generation, and
// writes it to leafPath whole. The leaf is tied to the inspect decision
// (#497), so the request carries a policy that inspects leafHost:443.
func (p *fakeProxyd) writeLeaf(vmName string, generation uint64) error {
	name, nameRef := dnsname.FromHost(leafHost)
	if nameRef != nil {
		return fmt.Errorf("fake wb-proxyd: %s: %w", leafHost, nameRef)
	}
	leaf, ref := p.authority.For(ca.LeafRequest{
		VM: vmName, Generation: generation, Name: leafHost, Port: 443,
		Policy: &policy.Policy{Endpoints: []policy.Endpoint{{Host: match.ExactHost(name), Ports: []uint16{443}}}},
	})
	if ref != nil {
		return fmt.Errorf("fake wb-proxyd: no leaf for %s (%s): %s", leafHost, ref.Rule, ref.Reason)
	}
	var b bytes.Buffer
	for _, der := range leaf.Certificate {
		if err := pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			return err
		}
	}
	key, err := x509.MarshalPKCS8PrivateKey(leaf.PrivateKey)
	if err != nil {
		return err
	}
	if err := pem.Encode(&b, &pem.Block{Type: "PRIVATE KEY", Bytes: key}); err != nil {
		return err
	}
	tmp := filepath.Join(p.dir, ".leaf.tmp")
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, leafPath(p.dir, generation))
}

// End logs the end the CA record asked for: a reply that broke the
// contract. The test fails on the record.
func (p *fakeProxyd) End(rule string) {
	p.log.Error(msgFakeEnded, "rule", rule)
}

// boot wraps the time hook send: a boot time call runs the CA record's
// boot order (S09-07-ca-certificates, "Boot, restore and host wake"),
// whose first step sets the guest clock with send. Its other calls are
// send's. A failed boot order is logged, and the link is kept unless
// the clock wasn't set, as with send alone.
func (p *fakeProxyd) boot(k *carecord.Keeper, send guestconn.TimeHook) guestconn.TimeHook {
	return func(ctx context.Context, slot vm.Slot, generation uint64, link *guestlink.Link, reason guestv1.TimeReason) error {
		if reason != guestv1.TimeReason_TIME_REASON_BOOT {
			return send(ctx, slot, generation, link, reason)
		}
		g := &linkGuest{send: send, slot: slot, generation: generation, link: link}
		err := k.Boot(ctx, string(slot), generation, carecord.ReasonBoot, g)
		if g.clockErr != nil {
			return g.clockErr
		}
		if err != nil {
			p.log.Error(msgBootFailed, "vm", string(slot), "generation", generation, "error", err.Error())
		}
		return nil
	}
}

// linkGuest is carecord.Guest over one guest link.
type linkGuest struct {
	send       guestconn.TimeHook
	slot       vm.Slot
	generation uint64
	link       *guestlink.Link
	clockErr   error
}

func (g *linkGuest) SetClock(ctx context.Context, _ carecord.Reason) error {
	g.clockErr = g.send(ctx, g.slot, g.generation, g.link, guestv1.TimeReason_TIME_REASON_BOOT)
	return g.clockErr
}

func (g *linkGuest) InstallTrust(ctx context.Context, certs [][]byte) error {
	_, err := g.link.Client().InstallTrust(ctx, &guestv1.InstallTrustRequest{CaCertificates: certs})
	return err
}
