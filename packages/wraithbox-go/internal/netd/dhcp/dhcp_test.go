package dhcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
	nt "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd/netdtest"
)

var (
	zero    = netip.IPv4Unspecified()
	other   = netip.MustParseAddr("192.168.127.9")
	offered = []byte{optMessageType, optServerID, optSubnetMask, optRouter, optDNS, optLeaseTime, optMTU}
	// Options the macOS client asks for that the server never offers
	// (S07-02-packet-path, "Addressing", X03-network-path).
	neverOffered = []byte{252, 121, 108, 162, 15, 119, 44, 95}
)

func lease(t testing.TB) Lease {
	t.Helper()
	cfg := nt.Config()
	l, err := LeaseOf(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func typ(m MessageType) []byte { return nt.DHCPOpt(optMessageType, byte(m)) }

func addrOpt(code byte, a netip.Addr) []byte {
	v := a.As4()
	return nt.DHCPOpt(code, v[:]...)
}

// allParams asks for every option code but PAD and END.
func allParams() []byte {
	p := make([]byte, 254)
	for i := range p {
		p[i] = byte(i + 1)
	}
	return nt.DHCPOpt(optParams, p...)
}

// macOSDiscover and macOSRequest are the macOS client's DISCOVER and
// REQUEST of X03-network-path, rebuilt from the fields its log holds:
// the xid, host name, parameter request list and requested address. The
// spike didn't log the raw bytes. Options 57 and 61 are added in the
// form RFC 2132 gives them.
func macOSDiscover(mac net.HardwareAddr) []byte {
	return nt.DHCP(0xb48c0d84, zero, mac, typ(MsgDiscover), macOSParams(),
		nt.DHCPOpt(optMaxSize, 0x05, 0xdc), nt.DHCPOpt(optClientID, append([]byte{1}, mac...)...),
		nt.DHCPOpt(optHostName, []byte("AppleViMachine1")...))
}

func macOSRequest(mac net.HardwareAddr, requested netip.Addr) []byte {
	return nt.DHCP(0x2347fbfc, zero, mac, typ(MsgRequest), macOSParams(),
		nt.DHCPOpt(optMaxSize, 0x05, 0xdc), nt.DHCPOpt(optClientID, append([]byte{1}, mac...)...),
		addrOpt(optRequestedIP, requested), addrOpt(optServerID, nt.Gateway),
		nt.DHCPOpt(optHostName, []byte("x03sVirlMachine")...))
}

// macOSParams is the parameter request list X03-network-path logged.
func macOSParams() []byte {
	return nt.DHCPOpt(optParams, 1, 3, 6, 15, 44, 46, 95, 108, 114, 119, 121, 162, 252)
}

// TestReply checks each row of the answer table of S07-02-packet-path,
// "Addressing".
func TestReply(t *testing.T) {
	l := lease(t)
	mac := nt.GuestMAC
	tests := []answerCase{
		{"DISCOVER gets OFFER", macOSDiscover(mac), MsgOffer, netd.RuleDHCPOffer, nt.Leased},
		{"REQUEST for the leased address gets ACK", macOSRequest(mac, nt.Leased), MsgAck, netd.RuleDHCPAck, nt.Leased},
		{"renewal from ciaddr gets ACK", nt.DHCP(1, nt.Leased, mac, typ(MsgRequest)), MsgAck, netd.RuleDHCPAck, nt.Leased},
		{"REQUEST for another address gets NAK", macOSRequest(mac, other), MsgNak, netd.RuleDHCPNak, zero},
		{"REQUEST with no address gets NAK", nt.DHCP(1, zero, mac, typ(MsgRequest)), MsgNak, netd.RuleDHCPNak, zero},
		{
			"REQUEST naming another server gets NAK",
			nt.DHCP(1, zero, mac, typ(MsgRequest), addrOpt(optRequestedIP, nt.Leased), addrOpt(optServerID, other)),
			MsgNak, netd.RuleDHCPNak, zero,
		},
		{"renewal from another ciaddr gets NAK", nt.DHCP(1, other, mac, typ(MsgRequest)), MsgNak, netd.RuleDHCPNak, zero},
		{"INFORM gets ACK with no address", nt.DHCP(1, nt.Leased, mac, typ(MsgInform)), MsgAck, netd.RuleDHCPInform, zero},
		{"RELEASE gets nothing", nt.DHCP(1, nt.Leased, mac, typ(MsgRelease)), 0, netd.RuleDHCPRelease, zero},
		{"DECLINE is a finding", nt.DHCP(1, zero, mac, typ(MsgDecline), addrOpt(optRequestedIP, nt.Leased)), 0, netd.RuleDHCPDecline, zero},
		{"OFFER from the guest", nt.DHCP(1, zero, mac, typ(MsgOffer)), 0, netd.RuleDHCPType, zero},
		{"ACK from the guest", nt.DHCP(1, zero, mac, typ(MsgAck)), 0, netd.RuleDHCPType, zero},
		{"NAK from the guest", nt.DHCP(1, zero, mac, typ(MsgNak)), 0, netd.RuleDHCPType, zero},
		{"LEASEQUERY", nt.DHCP(1, zero, mac, typ(10)), 0, netd.RuleDHCPType, zero},
		{"another MAC", macOSDiscover(nt.OtherMAC), 0, netd.RuleDHCPMAC, zero},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.check(t, l) })
	}
}

// answerCase is a row of the answer table: a request, the reply it gets,
// 0 for none, the rule it is logged with, and the reply's yiaddr.
type answerCase struct {
	name   string
	msg    []byte
	reply  MessageType
	rule   netd.Rule
	yiaddr netip.Addr
}

func (tc answerCase) check(t *testing.T, l Lease) {
	t.Helper()
	req, err := Parse(tc.msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := Decide(req, l); got.Reply != tc.reply || got.Rule != tc.rule {
		t.Fatalf("Decide = %+v, want reply %v rule %s", got, tc.reply, tc.rule)
	}
	reply, ok := Reply(req, l)
	if ok != (tc.reply != 0) {
		t.Fatalf("Reply ok = %v", ok)
	}
	if !ok {
		return
	}
	checkReply(t, reply, req, l)
	codes, opts, _ := nt.DHCPOptions(reply)
	if MessageType(opts[optMessageType][0]) != tc.reply {
		t.Fatalf("type %v, want %v", MessageType(opts[optMessageType][0]), tc.reply)
	}
	if y := nt.DHCPAddr(reply, nt.DHCPOffYIAddr); y != tc.yiaddr {
		t.Fatalf("yiaddr %s, want %s", y, tc.yiaddr)
	}
	want := offered
	if tc.reply == MsgNak {
		want = []byte{optMessageType, optServerID}
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("options %v, want %v", codes, want)
	}
}

// TestReplyOptions checks the values of an OFFER's options, and that a
// request for every option, the proxy configuration included, gets only
// the six (FR08-no-proxy-config).
func TestReplyOptions(t *testing.T) {
	l := lease(t)
	for _, m := range []MessageType{MsgDiscover, MsgRequest, MsgInform} {
		msg := nt.DHCP(7, zero, nt.GuestMAC, typ(m), allParams(), addrOpt(optRequestedIP, nt.Leased))
		req, err := Parse(msg)
		if err != nil {
			t.Fatal(err)
		}
		reply, ok := Reply(req, l)
		if !ok {
			t.Fatalf("%v: no reply", m)
		}
		_, opts, _ := nt.DHCPOptions(reply)
		for _, c := range neverOffered {
			if _, found := opts[c]; found {
				t.Fatalf("%v: the reply offers option %d", m, c)
			}
		}
		checkOfferedValues(t, opts)
		if binary.BigEndian.Uint32(reply[nt.DHCPOffXID:]) != 7 {
			t.Fatalf("%v: xid not copied", m)
		}
	}
}

// checkOfferedValues checks the values of the six options of the test
// link's lease.
func checkOfferedValues(t *testing.T, opts map[byte][]byte) {
	t.Helper()
	gw := nt.Gateway.As4()
	for code, want := range map[byte][]byte{
		optSubnetMask: {255, 255, 255, 0},
		optRouter:     gw[:],
		optDNS:        gw[:],
		optServerID:   gw[:],
		optLeaseTime:  binary.BigEndian.AppendUint32(nil, 3600),
		optMTU:        binary.BigEndian.AppendUint16(nil, uint16(netd.DefaultMTU)),
	} {
		if !bytes.Equal(opts[code], want) {
			t.Fatalf("option %d is %x, want %x", code, opts[code], want)
		}
	}
}

// TestParseRejects checks that each malformed request is dropped with
// dhcp-form, and that the reason says why.
func TestParseRejects(t *testing.T) {
	mac := nt.GuestMAC
	good := nt.DHCP(1, zero, mac, typ(MsgDiscover))
	with := func(off int, v byte) []byte {
		b := slices.Clone(good)
		b[off] = v
		return b
	}
	head := good[:nt.DHCPOffOpts]
	join := func(parts ...[]byte) []byte { return slices.Concat(append([][]byte{head}, parts...)...) }
	tests := []struct {
		name, reason string
		msg          []byte
	}{
		{"empty", "under", nil},
		{"fixed part cut", "under", good[:239]},
		{"too long", "over", append(slices.Clone(good), make([]byte, MaxMessage)...)},
		{"BOOTREPLY", "BOOTREQUEST", with(0, 2)},
		{"hardware type", "Ethernet", with(1, 6)},
		{"hardware length", "Ethernet", with(2, 16)},
		{"no cookie", "cookie", with(nt.DHCPOffCookie, 0)},
		{"no options", "no END", head},
		{"missing END", "no END", join(typ(MsgDiscover))},
		{"only padding", "no END", join(typ(MsgDiscover), []byte{0, 0, 0})},
		{"option with no length", "no length", join(typ(MsgDiscover), []byte{12})},
		{"option past the message", "runs past", join(typ(MsgDiscover), []byte{12, 40, 'a', 'b'})},
		{"no message type", "no message type", join([]byte{255})},
		{"option overload", "overload", join(typ(MsgDiscover), nt.DHCPOpt(optOverload, 3), []byte{255})},
		{"repeated message type", "repeats", join(typ(MsgDiscover), typ(MsgRequest), []byte{255})},
		{"message type of 2 bytes", "option 53 of", join(nt.DHCPOpt(optMessageType, 1, 1), []byte{255})},
		{"message type 0", "no message type", join(nt.DHCPOpt(optMessageType, 0), []byte{255})},
		{"short requested address", "option 50 of", join(typ(MsgRequest), nt.DHCPOpt(optRequestedIP, 1, 2, 3), []byte{255})},
		{"long server identifier", "option 54 of", join(typ(MsgRequest), nt.DHCPOpt(optServerID, 1, 2, 3, 4, 5), []byte{255})},
		{"empty parameter list", "option 55 of", join(typ(MsgDiscover), nt.DHCPOpt(optParams), []byte{255})},
		{"maximum size of 1 byte", "option 57 of", join(typ(MsgDiscover), nt.DHCPOpt(optMaxSize, 2), []byte{255})},
		{"maximum size under 576", "maximum", join(typ(MsgDiscover), nt.DHCPOpt(optMaxSize, 0, 200), []byte{255})},
		{"client identifier of 1 byte", "option 61 of", join(typ(MsgDiscover), nt.DHCPOpt(optClientID, 1), []byte{255})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.msg)
			var fe *FormError
			if !errors.As(err, &fe) {
				t.Fatalf("err %v, want a FormError", err)
			}
			if !strings.Contains(fe.Reason, tc.reason) {
				t.Fatalf("reason %q, want %q in it", fe.Reason, tc.reason)
			}
		})
	}
}

// TestParseAccepts checks what Parse lets through: padding, options it
// doesn't read, bytes after END, and the macOS client's messages.
func TestParseAccepts(t *testing.T) {
	mac := nt.GuestMAC
	for name, msg := range map[string][]byte{
		"padding":            nt.DHCP(1, zero, mac, []byte{0, 0}, typ(MsgDiscover), []byte{0}),
		"an unread option":   nt.DHCP(1, zero, mac, typ(MsgDiscover), nt.DHCPOpt(51, 0, 0, 1, 0), nt.DHCPOpt(250)),
		"bytes after END":    append(nt.DHCP(1, zero, mac, typ(MsgDiscover)), 1, 2, 3),
		"macOS DISCOVER":     macOSDiscover(mac),
		"macOS REQUEST":      macOSRequest(mac, nt.Leased),
		"largest message":    append(nt.DHCP(1, zero, mac, typ(MsgDiscover)), make([]byte, MaxMessage-244)...),
		"option at the edge": nt.DHCP(1, zero, mac, typ(MsgDiscover), nt.DHCPOpt(optHostName, make([]byte, 255)...)),
	} {
		if _, err := Parse(msg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestHandle checks that the server loop drops a request with its rule,
// logs every answer, and sends a reply to broadcast, or by unicast to a
// renewal from the leased address.
func TestHandle(t *testing.T) {
	l := lease(t)
	fromZero := &net.UDPAddr{IP: net.IPv4zero, Port: ClientPort}
	fromLeased := &net.UDPAddr{IP: nt.Leased.AsSlice(), Port: ClientPort}
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: ClientPort}
	tests := []handleCase{
		{"malformed", []byte("no"), fromZero, netd.RuleDHCPForm, nil},
		{"too long", make([]byte, MaxMessage+1), fromZero, netd.RuleDHCPForm, nil},
		{"another MAC", macOSDiscover(nt.OtherMAC), fromZero, netd.RuleDHCPMAC, nil},
		{"decline", nt.DHCP(1, zero, nt.GuestMAC, typ(MsgDecline)), fromZero, netd.RuleDHCPDecline, nil},
		{"unknown type", nt.DHCP(1, zero, nt.GuestMAC, typ(42)), fromZero, netd.RuleDHCPType, nil},
		{"release", nt.DHCP(1, nt.Leased, nt.GuestMAC, typ(MsgRelease)), fromLeased, netd.RuleDHCPRelease, nil},
		{"discover", macOSDiscover(nt.GuestMAC), fromZero, netd.RuleDHCPOffer, bcast},
		{"request", macOSRequest(nt.GuestMAC, nt.Leased), fromZero, netd.RuleDHCPAck, bcast},
		{"renewal", nt.DHCP(1, nt.Leased, nt.GuestMAC, typ(MsgRequest)), fromLeased, netd.RuleDHCPAck, fromLeased},
		{"nak", macOSRequest(nt.GuestMAC, other), fromZero, netd.RuleDHCPNak, bcast},
		{"inform", nt.DHCP(1, nt.Leased, nt.GuestMAC, typ(MsgInform)), fromLeased, netd.RuleDHCPInform, fromLeased},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.check(t, l) })
	}
}

// handleCase is a message the server loop gets, the rule it is logged
// with, and where its reply goes, nil for none.
type handleCase struct {
	name string
	msg  []byte
	from net.Addr
	rule netd.Rule
	to   *net.UDPAddr
}

func (tc handleCase) check(t *testing.T, l Lease) {
	t.Helper()
	logs := &capture{}
	rec := netd.NewRecorderWith(slog.New(logs), nil)
	conn := &fakeConn{}
	handle(conn, tc.msg, tc.from, l, rec)
	if rec.Count(tc.rule) != 1 {
		t.Fatalf("no %s", tc.rule)
	}
	if len(logs.records) != 1 {
		t.Fatalf("%d log records, want 1", len(logs.records))
	}
	if got := logs.records[0]["rule"]; got != string(tc.rule) {
		t.Fatalf("logged rule %q, want %s", got, tc.rule)
	}
	wantSent := 0
	if tc.to != nil {
		wantSent = 1
	}
	if len(conn.sent) != wantSent {
		t.Fatalf("sent %d replies, want %d", len(conn.sent), wantSent)
	}
	if tc.to != nil && conn.to[0].String() != tc.to.String() {
		t.Fatalf("sent the reply to %v, want %s", conn.to[0], tc.to)
	}
}

// TestHandleWriteFailed checks that a reply the stack doesn't take is
// logged with dhcp-send-failed.
func TestHandleWriteFailed(t *testing.T) {
	rec := netd.NewRecorderWith(slog.New(slog.DiscardHandler), nil)
	handle(&fakeConn{fail: true}, macOSDiscover(nt.GuestMAC), &net.UDPAddr{IP: net.IPv4zero, Port: ClientPort}, lease(t), rec)
	if rec.Count(netd.RuleDHCPSendFailed) != 1 || rec.Count(netd.RuleDHCPOffer) != 0 {
		t.Fatal("the failed write isn't logged as dhcp-send-failed")
	}
}

// TestLogEscapes checks that the strings from the guest are escaped and
// cut in the log record (S07-02-packet-path, "Addressing").
func TestLogEscapes(t *testing.T) {
	long := strings.Repeat("a", 200)
	msg := nt.DHCP(1, zero, nt.GuestMAC, typ(MsgDiscover),
		nt.DHCPOpt(optHostName, []byte("evil\n\"host\x1b[2J")...),
		nt.DHCPOpt(optVendorClass, []byte(long)...),
		nt.DHCPOpt(optClientID, 1, 0xff, 0),
		nt.DHCPOpt(optFQDN, []byte("\x00\x00\x00x\\y")...))
	logs := &capture{}
	handle(&fakeConn{}, msg, &net.UDPAddr{IP: net.IPv4zero, Port: ClientPort}, lease(t), netd.NewRecorderWith(slog.New(logs), nil))
	r := logs.records[0]
	for key, want := range map[string]string{
		"hostname":     `"evil\x0a\"host\x1b[2J"`,
		"vendor_class": `"` + long[:LogCut] + `"[cut from 200 bytes]`,
		"client_id":    `"\x01\xff\x00"`,
		"fqdn":         `"\x00\x00\x00x\\y"`,
	} {
		if r[key] != want {
			t.Errorf("%s logged as %s, want %s", key, r[key], want)
		}
	}
	checkLogged(t, logs)
}

// FuzzDHCP is the DHCP server's fuzz target (C01-network-and-dns,
// "Fuzzing"). It feeds bytes through Parse, Reply and the server loop,
// and checks that a reply goes only to the guest's MAC, holds only the
// leased address or zero in yiaddr and only the six options, and that
// every string in a log record is printable and within its cut.
func FuzzDHCP(f *testing.F) {
	mac := nt.GuestMAC
	for _, seed := range [][]byte{
		macOSDiscover(mac),
		macOSRequest(mac, nt.Leased),
		macOSRequest(mac, netip.MustParseAddr("10.0.2.15")),
		nt.DHCP(0, zero, mac, typ(MsgDecline), addrOpt(optRequestedIP, netip.MustParseAddr("10.0.2.15"))),
		nt.DHCP(1, nt.Leased, mac, typ(MsgInform), allParams()),
		nt.DHCP(1, nt.Leased, mac, typ(MsgRelease)),
		macOSDiscover(nt.OtherMAC),
		nt.DHCP(1, zero, mac, typ(MsgDiscover), nt.DHCPOpt(optOverload, 3)),
	} {
		f.Add(seed)
	}
	l := lease(f)
	from := &net.UDPAddr{IP: net.IPv4zero, Port: ClientPort}
	f.Fuzz(func(t *testing.T, msg []byte) {
		checkParsed(t, msg, l)
		logs := &capture{}
		conn := &fakeConn{}
		handle(conn, msg, from, l, netd.NewRecorderWith(slog.New(logs), nil))
		for _, r := range conn.sent {
			req, _ := Parse(msg)
			checkReply(t, r, req, l)
		}
		checkLogged(t, logs)
	})
}

// checkParsed checks that Parse fails only with a FormError, and that
// Reply answers only the guest's MAC, with a reply checkReply passes.
func checkParsed(t *testing.T, msg []byte, l Lease) {
	t.Helper()
	req, err := Parse(msg)
	if err != nil {
		if !errors.As(err, new(*FormError)) {
			t.Fatalf("Parse error %T", err)
		}
		return
	}
	reply, ok := Reply(req, l)
	if !ok {
		return
	}
	if !slices.Equal(req.CHAddr, l.MAC) {
		t.Fatalf("a reply to %s", req.CHAddr)
	}
	checkReply(t, reply, req, l)
}

// checkReply checks the properties every reply has: a BOOTREPLY to the
// guest's MAC with the request's xid, yiaddr the leased address or zero,
// and only the six options and the message type.
func checkReply(t *testing.T, reply []byte, req Request, l Lease) {
	t.Helper()
	if len(reply) < minReply || reply[offOp] != opReply {
		t.Fatalf("not a BOOTREPLY: %x", reply)
	}
	if !bytes.Equal(reply[offCHAddr:offCHAddr+6], l.MAC) {
		t.Fatalf("chaddr %x", reply[offCHAddr:offCHAddr+6])
	}
	if binary.BigEndian.Uint32(reply[offXID:]) != req.XID {
		t.Fatal("xid not copied")
	}
	if y := nt.DHCPAddr(reply, offYIAddr); y != l.Addr && y != zero {
		t.Fatalf("yiaddr %s", y)
	}
	codes, _, ok := nt.DHCPOptions(reply)
	if !ok {
		t.Fatalf("options don't parse: %x", reply)
	}
	for _, c := range codes {
		if !slices.Contains(offered, c) {
			t.Fatalf("option %d in a reply", c)
		}
	}
}

// maxLogged bounds a guest string in a log record: LogCut bytes of
// \xHH, two quotes, and the cut marker of a message of MaxMessage+1
// bytes.
var maxLogged = 4*LogCut + 2 + len("[cut from 1501 bytes]")

var guestStrings = map[string]bool{"hostname": true, "vendor_class": true, "client_id": true, "fqdn": true}

// checkLogged checks that every log record's attributes are printable
// ASCII, and that the strings from the guest are within maxLogged bytes.
func checkLogged(t *testing.T, logs *capture) {
	t.Helper()
	for _, r := range logs.records {
		for k, v := range r {
			if guestStrings[k] && len(v) > maxLogged {
				t.Fatalf("%s logged with %d bytes", k, len(v))
			}
			for i := range len(v) {
				if v[i] < 0x20 || v[i] >= 0x7f {
					t.Fatalf("%s logged with byte %#x: %q", k, v[i], v)
				}
			}
		}
	}
}

// capture is a slog handler that keeps each record's attributes as
// strings.
type capture struct {
	mu      sync.Mutex
	records []map[string]string
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }

func (c *capture) Handle(_ context.Context, r slog.Record) error {
	m := map[string]string{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, m)
	return nil
}

func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

// fakeConn keeps what is written to it, or fails every write.
type fakeConn struct {
	fail bool
	sent [][]byte
	to   []net.Addr
}

func (c *fakeConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }

func (c *fakeConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.fail {
		return 0, errors.New("write failed")
	}
	c.sent = append(c.sent, slices.Clone(b))
	c.to = append(c.to, addr)
	return len(b), nil
}

func (c *fakeConn) Close() error                     { return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return nil }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }
