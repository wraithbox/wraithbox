package dhcp

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/netd"
)

// LogCut is how many bytes of a string from the guest a log record
// keeps (S07-02-packet-path, "Events and rate limits"). A host name is
// at most 63 bytes a label.
const LogCut = 64

// LogAttrs returns the request as log attributes. The host name, vendor
// class, client identifier and FQDN come from the guest, so they go
// through guesttext.LogField, cut to LogCut bytes, never raw
// (S07-02-packet-path, "Addressing").
func (r *Request) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String("type", r.Type.String()),
		slog.String("xid", fmt.Sprintf("0x%08x", r.XID)),
		slog.String("chaddr", r.CHAddr.String()),
		slog.String("ciaddr", r.CIAddr.String()),
		slog.String("requested", addrOrNone(r.Requested)),
		slog.String("server_id", addrOrNone(r.ServerID)),
		slog.String("params", params(r.Params)),
		slog.String("hostname", guesttext.LogField(r.HostName, LogCut)),
		slog.String("vendor_class", guesttext.LogField(r.VendorClass, LogCut)),
		slog.String("client_id", guesttext.LogField(r.ClientID, LogCut)),
		slog.String("fqdn", guesttext.LogField(r.FQDN, LogCut)),
	}
}

func addrOrNone(a netip.Addr) string {
	if !a.IsValid() {
		return "none"
	}
	return a.String()
}

// params is the parameter request list as option codes, such as
// "1,3,6,252". It holds at most 255 codes of 3 digits.
func params(p []byte) string {
	s := make([]string, len(p))
	for i, c := range p {
		s[i] = strconv.Itoa(int(c))
	}
	return strings.Join(s, ",")
}

// Serve answers DHCP on conn, the stack's UDP port 67, until a read
// fails, which it does once conn is closed. It returns the read's error.
//
// Each request is logged once, with its rule, through rec, which limits
// the records per rule (S07-02-packet-path, "Events and rate limits",
// SEC13-bounded-resources). A drop is a Record, so Events gets it, and
// an answer or a RELEASE a Note.
func Serve(conn net.PacketConn, l Lease, rec *netd.Recorder) error {
	// One byte over MaxMessage, so a larger datagram fills the buffer
	// and Parse drops it.
	buf := make([]byte, MaxMessage+1)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		handle(conn, buf[:n], from, l, rec)
	}
}

// handle decides one message and sends its reply.
func handle(conn net.PacketConn, msg []byte, from net.Addr, l Lease, rec *netd.Recorder) {
	src := slog.String("from", from.String())
	req, err := Parse(msg)
	if err != nil {
		rec.Record(netd.Drop, netd.RuleDHCPForm, src, slog.Int("size", len(msg)), slog.String("reason", err.Error()))
		return
	}
	out := Decide(req, l)
	attrs := append([]slog.Attr{src}, req.LogAttrs()...)
	if out.Drop {
		rec.Record(netd.Drop, out.Rule, attrs...)
		return
	}
	reply, ok := Reply(req, l)
	if !ok {
		// RELEASE: logged, and the lease stays with the guest's MAC.
		rec.Note(out.Rule, attrs...)
		return
	}
	to := replyTo(from, out.Reply, l)
	attrs = append(attrs, slog.String("reply", out.Reply.String()), slog.String("to", to.String()))
	if _, err := conn.WriteTo(reply, to); err != nil {
		rec.Record(netd.Drop, netd.RuleDHCPSendFailed, append(attrs, slog.String("error", err.Error()))...)
		return
	}
	rec.Note(out.Rule, attrs...)
}

// replyTo is where a reply goes: by unicast to the leased address when
// the request came from it, as a renewal does, and otherwise to the
// limited broadcast address, since the guest has no address yet. A NAK
// is always broadcast (RFC 2131, section 4.1). Both on the client port.
func replyTo(from net.Addr, reply MessageType, l Lease) *net.UDPAddr {
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: ClientPort}
	if reply == MsgNak {
		return bcast
	}
	if u, ok := from.(*net.UDPAddr); ok {
		if a, ok := netip.AddrFromSlice(u.IP); ok && a.Unmap() == l.Addr {
			return &net.UDPAddr{IP: l.Addr.AsSlice(), Port: ClientPort}
		}
	}
	return bcast
}
