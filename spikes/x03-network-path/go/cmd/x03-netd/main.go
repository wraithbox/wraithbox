// X03-network-path spike: a throwaway wb-netd. Not held to the project gates.
//
// It receives the host end of the VM's file-handle network socket pair from
// x03vmd over a Unix socket (SCM_RIGHTS, as in X18-vsock-handoff), runs
// gVisor's userspace TCP/IP stack on it with a small link endpoint, a DHCP
// server, a DNS responder with a hard-coded allowlist that answers with
// synthetic addresses from 198.18.0.0/15, and accepts TCP only to synthetic
// addresses on allowed ports. Accepted streams are relayed to a stub (the
// wb-proxyd stand-in) over a Unix socket. Every decision is a JSON line on
// stdout with the rule that made it.
//
// A host-only debug forward (127.0.0.1:<port> -> guest:22) lets the spike
// drive the guest over SSH. The product has no such path.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const nicID = 1

var (
	gwIP     = net.IPv4(10, 0, 2, 2).To4()
	guestIP  = net.IPv4(10, 0, 2, 15).To4()
	gwMAC    = net.HardwareAddr{0x02, 0x57, 0x42, 0x00, 0x00, 0x01}
	synthNet = &net.IPNet{IP: net.IPv4(198, 18, 0, 0).To4(), Mask: net.CIDRMask(15, 32)}
)

var (
	outMu sync.Mutex
	t0    = time.Now()
)

// ev writes one JSON line: an event or decision, with the rule that made it.
func ev(kind string, kv map[string]any) {
	if kv == nil {
		kv = map[string]any{}
	}
	kv["t"] = time.Since(t0).Seconds()
	kv["wall"] = time.Now().UTC().Format(time.RFC3339Nano)
	kv["ev"] = kind
	b, _ := json.Marshal(kv)
	outMu.Lock()
	os.Stdout.Write(append(b, '\n'))
	outMu.Unlock()
}

// counters for the link filter, printed periodically.
type counters struct {
	sync.Mutex
	m map[string]uint64
}

func (c *counters) inc(k string) uint64 {
	c.Lock()
	defer c.Unlock()
	c.m[k]++
	return c.m[k]
}

func (c *counters) snapshot() map[string]uint64 {
	c.Lock()
	defer c.Unlock()
	r := make(map[string]uint64, len(c.m))
	for k, v := range c.m {
		r[k] = v
	}
	return r
}

var cnt = &counters{m: map[string]uint64{}}

// drop counts a dropped frame and logs the first few of each rule.
func drop(rule string, kv map[string]any) {
	if n := cnt.inc("drop:" + rule); n <= 5 || n%1000 == 0 {
		if kv == nil {
			kv = map[string]any{}
		}
		kv["rule"] = rule
		kv["count"] = n
		ev("drop", kv)
	}
}

// ---- configuration ----

type cfg struct {
	sock      string
	guestMAC  net.HardwareAddr
	mtu       int
	allow     []string
	ports     map[uint16]bool
	stubSock  string
	sshListen string
	stubFile  string
	stubTCP   string
	dhcpMTU   bool
	// unfiltered turns off the ARP rule of the link filter, unfilteredICMP the ICMP rule, to
	// see what gVisor alone answers in promiscuous and spoofing mode.
	unfiltered, unfilteredICMP bool
}

// relayBuf is the copy buffer between the guest stream and the stub; 0
// means io.Copy's default (32 KiB).
var relayBuf int

func main() {
	var c cfg
	var mac, allow, ports string
	flag.StringVar(&c.sock, "sock", "", "unix socket to receive the NIC descriptor on")
	flag.StringVar(&mac, "mac", "", "guest MAC")
	flag.IntVar(&c.mtu, "mtu", 1500, "MTU (IP), must match the attachment")
	flag.BoolVar(&c.dhcpMTU, "dhcp-mtu", true, "send DHCP option 26 (interface MTU)")
	flag.BoolVar(&c.unfiltered, "unfiltered", false, "turn off the ARP link rule (experiment only)")
	flag.BoolVar(&c.unfilteredICMP, "unfiltered-icmp", false, "turn off the ICMP link rule (experiment only)")
	flag.IntVar(&relayBuf, "relay-buf", 0, "relay copy buffer in bytes (0: io.Copy default)")
	flag.StringVar(&allow, "allow", "bulk.test,allowed.test", "comma-separated allowlist; *.x for wildcards")
	flag.StringVar(&ports, "ports", "80,443", "allowed TCP ports to synthetic addresses")
	flag.StringVar(&c.stubSock, "stub", "", "unix socket for the stub (wb-proxyd stand-in); started in-process")
	flag.StringVar(&c.stubFile, "stub-file", "", "file the stub serves at /1g")
	flag.StringVar(&c.stubTCP, "stub-tcp", "127.0.0.1:18080", "also serve the stub on host TCP, for the host baseline")
	flag.StringVar(&c.sshListen, "ssh", "127.0.0.1:2222", "host-only debug forward to guest:22")
	flag.Parse()
	var err error
	if c.guestMAC, err = net.ParseMAC(mac); err != nil {
		fmt.Fprintln(os.Stderr, "bad -mac:", err)
		os.Exit(2)
	}
	c.allow = strings.Split(allow, ",")
	c.ports = map[uint16]bool{}
	for _, p := range strings.Split(ports, ",") {
		n, _ := strconv.Atoi(p)
		c.ports[uint16(n)] = true
	}
	if c.stubSock != "" {
		go runStub(c.stubSock, c.stubFile, c.stubTCP)
	}
	fd, err := receiveFD(c.sock)
	if err != nil {
		ev("fatal", map[string]any{"err": err.Error()})
		os.Exit(1)
	}
	run(c, fd)
}

// receiveFD accepts one connection on a Unix socket and returns the one
// descriptor that arrives with the first message.
func receiveFD(path string) (int, error) {
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return -1, err
	}
	defer l.Close()
	ev("listening", map[string]any{"sock": path})
	conn, err := l.Accept()
	if err != nil {
		return -1, err
	}
	uc := conn.(*net.UnixConn)
	buf := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return -1, err
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return -1, err
	}
	var fds []int
	for _, m := range msgs {
		f, err := syscall.ParseUnixRights(&m)
		if err != nil {
			return -1, err
		}
		fds = append(fds, f...)
	}
	if len(fds) != 1 {
		for _, f := range fds {
			syscall.Close(f)
		}
		return -1, fmt.Errorf("want exactly one descriptor, got %d", len(fds))
	}
	fd := fds[0]
	st, _ := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if st != syscall.SOCK_DGRAM {
		syscall.Close(fd)
		return -1, fmt.Errorf("descriptor is socket type %d, want SOCK_DGRAM", st)
	}
	ev("nic-fd", map[string]any{"fd": fd, "msg": string(buf[:n])})
	uc.Write([]byte("ok\n"))
	go func() { io.Copy(io.Discard, uc); ev("vmd-closed", nil) }()
	return fd, nil
}

// ---- the stack ----

func run(c cfg, fd int) {
	// Apple's header for the attachment: SO_RCVBUF at least twice
	// SO_SNDBUF, four times recommended.
	for o, v := range map[int]int{syscall.SO_SNDBUF: 1 << 20, syscall.SO_RCVBUF: 4 << 20} {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, o, v); err != nil {
			ev("setsockopt", map[string]any{"opt": o, "err": err.Error()})
		}
	}
	snd, _ := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	rcv, _ := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	ev("sockbuf", map[string]any{"snd": snd, "rcv": rcv})

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4},
	})
	// Bigger TCP buffers for throughput.
	{
		opt := tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: 1 << 20, Max: 8 << 20}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
		ropt := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: 1 << 20, Max: 8 << 20}
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &ropt)
		sack := tcpip.TCPSACKEnabled(true)
		s.SetTransportProtocolOption(tcp.ProtocolNumber, &sack)
	}
	ch := channel.New(4096, uint32(c.mtu+header.EthernetMinimumSize), tcpip.LinkAddress(gwMAC))
	ep := ethernet.New(ch)
	if err := s.CreateNICWithOptions(nicID, ep, stack.NICOptions{Name: "guest0"}); err != nil {
		panic(err.String())
	}
	s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4Slice(gwIP), PrefixLen: 24},
	}, stack.AddressProperties{})
	// Promiscuous and spoofing let the stack accept and answer for the
	// synthetic addresses. The link filter below keeps ARP and ICMP to the
	// gateway address only.
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})

	d := newDNS(c.allow)
	tcpFwd := tcp.NewForwarder(s, 0, 1024, func(r *tcp.ForwarderRequest) { handleTCP(s, c, d, r) })
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		rule := "udp-not-dns"
		if id.LocalPort == 53 {
			rule = "dns-other-server"
		}
		drop(rule, map[string]any{"dst": id.LocalAddress.String(), "dport": id.LocalPort, "src": id.RemoteAddress.String(), "sport": id.RemotePort})
		return true // consumed silently, no ICMP port unreachable
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	go linkOut(ch, fd)
	go linkIn(ch, fd, c)
	go serveDHCP(s, c)
	go d.serveUDP(s)
	go d.serveTCP(s)
	if c.sshListen != "" {
		go sshForward(s, c.sshListen)
	}
	go func() {
		for range time.Tick(30 * time.Second) {
			ev("counters", map[string]any{"c": cnt.snapshot()})
		}
	}()
	select {}
}

// linkIn reads one Ethernet frame per datagram from the socket pair,
// filters it, and injects it into the stack.
func linkIn(ch *channel.Endpoint, fd int, c cfg) {
	buf := make([]byte, 65536+64)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			ev("link-read-error", map[string]any{"err": err.Error()})
			return
		}
		if n == 0 {
			continue
		}
		cnt.inc("in:frames")
		f := buf[:n]
		if !filterIn(f, c) {
			continue
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), f...))})
		ch.InjectInbound(0, pkt)
		pkt.DecRef()
	}
}

// filterIn is the link-level policy: guest MAC only, IPv4 and ARP only,
// guest source address only, ARP and ICMP only for the gateway.
func filterIn(f []byte, c cfg) bool {
	if len(f) < header.EthernetMinimumSize {
		drop("runt", map[string]any{"len": len(f)})
		return false
	}
	if len(f) > c.mtu+header.EthernetMinimumSize {
		drop("oversize", map[string]any{"len": len(f), "mtu": c.mtu})
		return false
	}
	eth := header.Ethernet(f)
	src := net.HardwareAddr([]byte(eth.SourceAddress()))
	if src.String() != c.guestMAC.String() {
		drop("mac-mismatch", map[string]any{"src": src.String()})
		return false
	}
	p := f[header.EthernetMinimumSize:]
	switch eth.Type() {
	case header.ARPProtocolNumber:
		a := header.ARP(p)
		if !a.IsValid() {
			drop("arp-invalid", nil)
			return false
		}
		spa := net.IP(a.ProtocolAddressSender())
		tpa := net.IP(a.ProtocolAddressTarget())
		if !(spa.Equal(guestIP) || spa.Equal(net.IPv4zero)) {
			drop("arp-spoof", map[string]any{"spa": spa.String()})
			return false
		}
		if a.Op() == header.ARPRequest && !tpa.Equal(gwIP) && !c.unfiltered {
			// Includes the guest's own probes for its leased address,
			// which must go unanswered.
			drop("arp-not-gateway", map[string]any{"tpa": tpa.String(), "spa": spa.String()})
			return false
		}
		return true
	case header.IPv4ProtocolNumber:
		ip := header.IPv4(p)
		if !ip.IsValid(len(p)) {
			drop("ipv4-invalid", nil)
			return false
		}
		s, d := net.IP(ip.SourceAddressSlice()), net.IP(ip.DestinationAddressSlice())
		proto := ip.TransportProtocol()
		var sport, dport uint16
		if (proto == header.UDPProtocolNumber || proto == header.TCPProtocolNumber) && len(ip.Payload()) >= 4 {
			sport = binary.BigEndian.Uint16(ip.Payload()[0:2])
			dport = binary.BigEndian.Uint16(ip.Payload()[2:4])
		}
		isDHCP := proto == header.UDPProtocolNumber && sport == 68 && dport == 67
		if !s.Equal(guestIP) && !(isDHCP && s.Equal(net.IPv4zero)) {
			drop("ip-spoof", map[string]any{"src": s.String(), "dst": d.String(), "proto": proto})
			return false
		}
		bcast := d.Equal(net.IPv4bcast) || d.Equal(net.IPv4(10, 0, 2, 255))
		if bcast && !isDHCP {
			drop("broadcast-not-dhcp", map[string]any{"dst": d.String(), "proto": proto, "dport": dport})
			return false
		}
		if d.IsMulticast() {
			drop("multicast", map[string]any{"dst": d.String(), "proto": proto, "dport": dport})
			return false
		}
		switch proto {
		case header.ICMPv4ProtocolNumber:
			if !d.Equal(gwIP) && !c.unfilteredICMP {
				drop("icmp-not-gateway", map[string]any{"dst": d.String()})
				return false
			}
		case header.UDPProtocolNumber:
			if !(d.Equal(gwIP) && dport == 53) && !isDHCP {
				rule := "udp-not-dns"
				if dport == 53 {
					rule = "dns-other-server"
				} else if dport == 443 {
					rule = "udp-quic"
				}
				drop(rule, map[string]any{"dst": d.String(), "dport": dport})
				return false
			}
		case header.TCPProtocolNumber:
			// Passed to the stack: the forwarder resets anything it
			// does not accept, so clients fail fast.
		default:
			drop("ip-proto", map[string]any{"proto": proto, "dst": d.String()})
			return false
		}
		return true
	case header.IPv6ProtocolNumber:
		var nh uint8
		if len(p) >= 40 {
			nh = p[6]
		}
		drop("ipv6", map[string]any{"next": nh})
		return false
	default:
		drop("ethertype", map[string]any{"type": fmt.Sprintf("%#04x", uint16(eth.Type()))})
		return false
	}
}

// linkOut writes each outbound frame as one datagram.
func linkOut(ch *channel.Endpoint, fd int) {
	ctx := context.Background()
	for {
		pkt := ch.ReadContext(ctx)
		if pkt == nil {
			return
		}
		v := pkt.ToView()
		b := v.AsSlice()
		for {
			_, err := syscall.Write(fd, b)
			if err == nil {
				break
			}
			if errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				cnt.inc("out:retry")
				time.Sleep(20 * time.Microsecond)
				continue
			}
			cnt.inc("out:err")
			ev("link-write-error", map[string]any{"err": err.Error(), "len": len(b)})
			break
		}
		cnt.inc("out:frames")
		v.Release()
		pkt.DecRef()
	}
}

// ---- DHCP ----

func serveDHCP(s *stack.Stack, c cfg) {
	var wq waiter.Queue
	ep, terr := s.NewEndpoint(udp.ProtocolNumber, ipv4.ProtocolNumber, &wq)
	if terr != nil {
		panic(terr.String())
	}
	ep.SocketOptions().SetBroadcast(true)
	if terr := ep.Bind(tcpip.FullAddress{NIC: nicID, Port: 67}); terr != nil {
		panic(terr.String())
	}
	conn := gonet.NewUDPConn(&wq, ep)
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			ev("dhcp-read-error", map[string]any{"err": err.Error()})
			return
		}
		req, err := dhcpv4.FromBytes(buf[:n])
		if err != nil {
			drop("dhcp-malformed", map[string]any{"err": err.Error()})
			continue
		}
		if req.ClientHWAddr.String() != c.guestMAC.String() {
			drop("dhcp-wrong-mac", map[string]any{"chaddr": req.ClientHWAddr.String()})
			continue
		}
		mt := req.MessageType()
		ev("dhcp-in", map[string]any{"type": mt.String(), "from": from.String(), "xid": req.TransactionID.String(),
			"requested": req.RequestedIPAddress().String(), "ciaddr": req.ClientIPAddr.String(),
			"params": fmt.Sprint(req.ParameterRequestList()), "hostname": req.HostName()})
		var reply dhcpv4.MessageType
		switch mt {
		case dhcpv4.MessageTypeDiscover:
			reply = dhcpv4.MessageTypeOffer
		case dhcpv4.MessageTypeRequest:
			r := req.RequestedIPAddress()
			if r == nil || r.IsUnspecified() {
				r = req.ClientIPAddr
			}
			if r.Equal(guestIP) {
				reply = dhcpv4.MessageTypeAck
			} else {
				reply = dhcpv4.MessageTypeNak
			}
		case dhcpv4.MessageTypeInform:
			reply = dhcpv4.MessageTypeAck
		default:
			ev("dhcp-ignored", map[string]any{"type": mt.String()})
			continue
		}
		mods := []dhcpv4.Modifier{
			dhcpv4.WithMessageType(reply),
			dhcpv4.WithServerIP(gwIP),
			dhcpv4.WithOption(dhcpv4.OptServerIdentifier(gwIP)),
		}
		if reply != dhcpv4.MessageTypeNak {
			mods = append(mods,
				dhcpv4.WithYourIP(guestIP),
				dhcpv4.WithNetmask(net.CIDRMask(24, 32)),
				dhcpv4.WithRouter(gwIP),
				dhcpv4.WithDNS(gwIP),
				dhcpv4.WithLeaseTime(3600),
			)
			if c.dhcpMTU {
				mtu := make([]byte, 2)
				binary.BigEndian.PutUint16(mtu, uint16(c.mtu))
				mods = append(mods, dhcpv4.WithOption(dhcpv4.Option{Code: dhcpv4.OptionInterfaceMTU, Value: dhcpv4.OptionGeneric{Data: mtu}}))
			}
			if mt == dhcpv4.MessageTypeInform {
				mods[3] = dhcpv4.WithYourIP(net.IPv4zero)
			}
		}
		resp, err := dhcpv4.NewReplyFromRequest(req, mods...)
		if err != nil {
			ev("dhcp-reply-error", map[string]any{"err": err.Error()})
			continue
		}
		if _, err := conn.WriteTo(resp.ToBytes(), &net.UDPAddr{IP: net.IPv4bcast, Port: 68}); err != nil {
			ev("dhcp-write-error", map[string]any{"err": err.Error()})
			continue
		}
		ev("dhcp-out", map[string]any{"type": reply.String(), "yiaddr": resp.YourIPAddr.String(), "mtuOpt": c.dhcpMTU, "mtu": c.mtu})
	}
}

// ---- DNS ----

type dnsServer struct {
	mu     sync.Mutex
	allow  []string
	byName map[string]net.IP
	byIP   map[string]string
	next   uint32
}

func newDNS(allow []string) *dnsServer {
	return &dnsServer{allow: allow, byName: map[string]net.IP{}, byIP: map[string]string{}, next: binary.BigEndian.Uint32(synthNet.IP) + 1}
}

func (d *dnsServer) allowed(name string) (bool, string) {
	for _, a := range d.allow {
		if a == "" {
			continue
		}
		if a == "*" {
			return true, "learn-mode" // FR10-learn-mode stand-in: every name resolves
		}
		if strings.HasPrefix(a, "*.") {
			if strings.HasSuffix(name, a[1:]) {
				return true, "allow-wildcard:" + a
			}
		} else if name == a {
			return true, "allow:" + a
		}
	}
	return false, "not-allowlisted"
}

func (d *dnsServer) synth(name string) net.IP {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ip, ok := d.byName[name]; ok {
		return ip
	}
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, d.next)
	d.next++
	d.byName[name] = ip
	d.byIP[ip.String()] = name
	return ip
}

func (d *dnsServer) nameFor(ip net.IP) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.byIP[ip.String()]
	return n, ok
}

// answer builds the response to one query message.
func (d *dnsServer) answer(q []byte, transport string) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		drop("dns-malformed", map[string]any{"err": err.Error(), "transport": transport})
		return nil
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 {
		drop("dns-malformed", map[string]any{"questions": len(qs), "transport": transport})
		return nil
	}
	qq := qs[0]
	name := strings.TrimSuffix(strings.ToLower(qq.Name.String()), ".")
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true}
	var ans []dnsmessage.Resource
	ok, rule := d.allowed(name)
	switch {
	case qq.Class != dnsmessage.ClassINET:
		rh.RCode, rule = dnsmessage.RCodeRefused, "class-not-in"
	case !ok:
		rh.RCode = dnsmessage.RCodeNameError
	case qq.Type == dnsmessage.TypeA:
		ip := d.synth(name)
		ans = append(ans, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: qq.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
			Body:   &dnsmessage.AResource{A: [4]byte(ip.To4())},
		})
	case qq.Type == dnsmessage.TypeAAAA:
		rule += ";aaaa-empty" // NOERROR, no answer
	default:
		rh.RCode = dnsmessage.RCodeRefused
		rule += ";type-refused"
	}
	ev("dns", map[string]any{"name": name, "type": qq.Type.String(), "rcode": rh.RCode.String(), "rule": rule, "answers": len(ans), "transport": transport})
	b := dnsmessage.NewBuilder(make([]byte, 0, 512), rh)
	b.EnableCompression()
	b.StartQuestions()
	b.Question(qq)
	b.StartAnswers()
	for _, a := range ans {
		b.AResource(a.Header, *a.Body.(*dnsmessage.AResource))
	}
	out, err := b.Finish()
	if err != nil {
		ev("dns-build-error", map[string]any{"err": err.Error()})
		return nil
	}
	return out
}

func (d *dnsServer) serveUDP(s *stack.Stack) {
	conn, err := gonet.DialUDP(s, &tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4Slice(gwIP), Port: 53}, nil, ipv4.ProtocolNumber)
	if err != nil {
		panic(err)
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			ev("dns-read-error", map[string]any{"err": err.Error()})
			return
		}
		if out := d.answer(buf[:n], "udp"); out != nil {
			conn.WriteTo(out, from)
		}
	}
}

func (d *dnsServer) serveTCP(s *stack.Stack) {
	l, err := gonet.ListenTCP(s, tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4Slice(gwIP), Port: 53}, ipv4.ProtocolNumber)
	if err != nil {
		panic(err)
	}
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(c)
			for {
				var l [2]byte
				if _, err := io.ReadFull(r, l[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(r, q); err != nil {
					return
				}
				out := d.answer(q, "tcp")
				if out == nil {
					return
				}
				binary.BigEndian.PutUint16(l[:], uint16(len(out)))
				c.Write(append(l[:], out...))
			}
		}()
	}
}

// ---- TCP ----

var streamID atomic.Uint64

func handleTCP(s *stack.Stack, c cfg, d *dnsServer, r *tcp.ForwarderRequest) {
	id := r.ID()
	dst := net.IP(id.LocalAddress.AsSlice())
	info := map[string]any{"dst": dst.String(), "dport": id.LocalPort, "sport": id.RemotePort}
	if !synthNet.Contains(dst) {
		info["rule"] = "not-synthetic"
		ev("tcp-reset", info)
		r.Complete(true)
		return
	}
	name, ok := d.nameFor(dst)
	if !ok {
		info["rule"] = "synthetic-unallocated"
		ev("tcp-reset", info)
		r.Complete(true)
		return
	}
	info["host"] = name
	if !c.ports[id.LocalPort] {
		info["rule"] = "port-not-allowed"
		ev("tcp-reset", info)
		r.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	if terr != nil {
		info["err"] = terr.String()
		ev("tcp-create-error", info)
		r.Complete(true)
		return
	}
	r.Complete(false)
	ep.SocketOptions().SetDelayOption(false)
	gc := gonet.NewTCPConn(&wq, ep)
	sid := streamID.Add(1)
	info["stream"] = sid
	info["rule"] = "synthetic-allowed-port"
	ev("tcp-accept", info)
	go relayToStub(gc, c.stubSock, sid, name, id.LocalPort)
}

// serveZeros answers one HTTP request with 1 GiB of zeros from memory, so
// the stack's own ceiling can be measured without the stub's Unix socket.
func serveZeros(gc *gonet.TCPConn, sid uint64) {
	defer gc.Close()
	r := bufio.NewReader(gc)
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if l == "\r\n" {
			break
		}
	}
	const total = 1 << 30
	start := time.Now()
	fmt.Fprintf(gc, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", total)
	buf := make([]byte, 1<<20)
	var n int64
	for n < total {
		w, err := gc.Write(buf)
		n += int64(w)
		if err != nil {
			break
		}
	}
	gc.CloseWrite()
	el := time.Since(start).Seconds()
	ev("tcp-done", map[string]any{"stream": sid, "host": "zero.test", "bytesToGuest": n, "seconds": el, "MBps": float64(n) / el / 1e6})
}

// relayCopy copies with a relayBuf-sized buffer. The wrappers hide
// ReaderFrom/WriterTo, which would otherwise bypass the buffer.
func relayCopy(dst io.Writer, src io.Reader) (int64, error) {
	if relayBuf == 0 {
		return io.Copy(dst, src)
	}
	return io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, make([]byte, relayBuf))
}

func relayToStub(gc *gonet.TCPConn, stubSock string, sid uint64, host string, port uint16) {
	if host == "zero.test" {
		serveZeros(gc, sid)
		return
	}
	defer gc.Close()
	up, err := net.Dial("unix", stubSock)
	if err != nil {
		ev("stub-dial-error", map[string]any{"stream": sid, "err": err.Error()})
		return
	}
	defer up.Close()
	start := time.Now()
	var down, upb int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		upb, _ = relayCopy(up, gc)
		up.(*net.UnixConn).CloseWrite()
	}()
	down, _ = relayCopy(gc, up)
	gc.CloseWrite()
	wg.Wait()
	el := time.Since(start).Seconds()
	ev("tcp-done", map[string]any{"stream": sid, "host": host, "port": port, "bytesToGuest": down, "bytesFromGuest": upb, "seconds": el, "MBps": float64(down) / el / 1e6})
}

// ---- stub (wb-proxyd stand-in) ----

func runStub(sock, file, tcpAddr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/1g", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, file)
	})
	mux.HandleFunc("/sink", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		n, _ := io.Copy(io.Discard, r.Body)
		el := time.Since(start).Seconds()
		ev("stub-sink", map[string]any{"bytesFromGuest": n, "seconds": el, "MBps": float64(n) / el / 1e6})
		fmt.Fprintf(w, "%d\n", n)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "stub: %s %s host=%s\n", r.Method, r.URL.Path, r.Host)
	})
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		ev("stub-error", map[string]any{"err": err.Error()})
		return
	}
	if tcpAddr != "" {
		tl, err := net.Listen("tcp", tcpAddr)
		if err == nil {
			go http.Serve(tl, mux)
		}
	}
	http.Serve(l, mux)
}

// ---- host-only SSH debug forward ----

func sshForward(s *stack.Stack, listen string) {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		ev("ssh-listen-error", map[string]any{"err": err.Error()})
		return
	}
	for {
		hc, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer hc.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			gc, err := gonet.DialContextTCP(ctx, s, tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4Slice(guestIP), Port: 22}, ipv4.ProtocolNumber)
			cancel()
			if err != nil {
				ev("ssh-dial-error", map[string]any{"err": err.Error()})
				return
			}
			defer gc.Close()
			go io.Copy(gc, hc)
			io.Copy(hc, gc)
		}()
	}
}
