package egressmsg

import (
	"math"
	"net/netip"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

// NetdKind is the kind of a NetdMessage.
type NetdKind int

// The kinds of NetdMessage. The zero NetdKind is no kind.
const (
	KindNameReport NetdKind = iota + 1
	KindRelease
	KindStream
)

// String returns the kind's name, for log records.
func (k NetdKind) String() string {
	switch k {
	case KindNameReport:
		return "name-report"
	case KindRelease:
		return "release"
	case KindStream:
		return "stream"
	}
	return "none"
}

// NetdMessage is a checked message from wb-netd on a hand-off socket.
// Only the fields of its kind are set.
type NetdMessage struct {
	Kind NetdKind
	// Name is a name report's name, in canonical form.
	Name dnsname.Name
	// Addr is the address of every kind, inside SyntheticRange.
	Addr netip.Addr
	// Generation, Port and StreamID are a stream's, each at least 1.
	Generation uint64
	Port       uint16
	StreamID   uint64
}

// DecodeNetdMessage checks one record that wb-proxyd read from a
// hand-off socket, and whether a descriptor came with it, against the
// comments in handoff.proto. The record is guest-influenced input
// (S07-05-stream-hand-off, "Message form"). A refusal of any check is a
// malformed message (S07-06-name-mapping, "Malformed reports").
func DecodeNetdMessage(record []byte, hasFD bool) (NetdMessage, *Refusal) {
	m := &egressv1.NetdMessage{}
	if r := decode(record, m); r != nil {
		return NetdMessage{}, r
	}
	switch k := m.GetKind().(type) {
	case *egressv1.NetdMessage_NameReport:
		return nameReport(k.NameReport, hasFD)
	case *egressv1.NetdMessage_Release:
		return release(k.Release, hasFD)
	case *egressv1.NetdMessage_Stream:
		return stream(k.Stream, hasFD)
	}
	return NetdMessage{}, refuse(CheckMessageForm, "kind", "no kind set")
}

func nameReport(m *egressv1.NameReport, hasFD bool) (NetdMessage, *Refusal) {
	if r := descriptors("name_report", false, hasFD); r != nil {
		return NetdMessage{}, r
	}
	name, r := Name("name_report.name", m.GetName())
	if r != nil {
		return NetdMessage{}, r
	}
	addr, r := Addr("name_report.address", m.GetAddress())
	if r != nil {
		return NetdMessage{}, r
	}
	return NetdMessage{Kind: KindNameReport, Name: name, Addr: addr}, nil
}

func release(m *egressv1.Release, hasFD bool) (NetdMessage, *Refusal) {
	if r := descriptors("release", false, hasFD); r != nil {
		return NetdMessage{}, r
	}
	addr, r := Addr("release.address", m.GetAddress())
	if r != nil {
		return NetdMessage{}, r
	}
	return NetdMessage{Kind: KindRelease, Addr: addr}, nil
}

func stream(m *egressv1.Stream, hasFD bool) (NetdMessage, *Refusal) {
	if r := descriptors("stream", true, hasFD); r != nil {
		return NetdMessage{}, r
	}
	if r := atLeastOne("stream.generation", m.GetGeneration()); r != nil {
		return NetdMessage{}, r
	}
	if r := atLeastOne("stream.stream_id", m.GetStreamId()); r != nil {
		return NetdMessage{}, r
	}
	port := m.GetPort()
	if port == 0 || port > math.MaxUint16 {
		return NetdMessage{}, refuse(CheckMessageForm, "stream.port", "is outside 1 to 65535")
	}
	addr, r := Addr("stream.address", m.GetAddress())
	if r != nil {
		return NetdMessage{}, r
	}
	return NetdMessage{
		Kind: KindStream, Addr: addr,
		Generation: m.GetGeneration(), Port: uint16(port), StreamID: m.GetStreamId(),
	}, nil
}

// DecodeProxydMessage checks one record that wb-netd read from a
// hand-off socket, and whether a descriptor came with it, and returns
// the stream id of the StreamReset it holds, at least 1.
func DecodeProxydMessage(record []byte, hasFD bool) (streamID uint64, ref *Refusal) {
	m := &egressv1.ProxydMessage{}
	if r := decode(record, m); r != nil {
		return 0, r
	}
	reset := m.GetStreamReset()
	if reset == nil {
		return 0, refuse(CheckMessageForm, "kind", "no kind set")
	}
	if r := descriptors("stream_reset", false, hasFD); r != nil {
		return 0, r
	}
	if r := atLeastOne("stream_reset.stream_id", reset.GetStreamId()); r != nil {
		return 0, r
	}
	return reset.GetStreamId(), nil
}
