// hostrecv: the wb-hostd / wb-netd stand-in for X18-vsock-handoff. Throwaway.
//
// Listens on a Unix socket. The wb-vmd stand-in (host-vmd, Swift) sends one
// JSON request per message, with a descriptor attached as SCM_RIGHTS when it
// hands one off, and reads one JSON line back. vsock descriptors become gRPC
// client connections; network descriptors carry ICMPv6 echo to the guest.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"x18/vs"
)

type req struct {
	Op        string `json:"op"`
	ID        string `json:"id"`
	TimeoutMs int    `json:"timeoutMs"`
	WaitMs    int    `json:"waitMs"`
	BulkBytes int    `json:"bulkBytes"`
	Port      int    `json:"port"`
	Reverse   bool   `json:"reverse"`
	Once      bool   `json:"once"`
}

type vconn struct {
	conn  *vs.Conn
	cc    *grpc.ClientConn
	mu    sync.Mutex
	watch []string
}

type nconn struct {
	f      *os.File
	fd     int
	mu     sync.Mutex
	seq    uint16
	frames []frameRec
	rerr   string
}

type frameRec struct {
	t       time.Time
	summary string
	seq     int // echo reply seq, or -1
}

var (
	mu     sync.Mutex
	vconns = map[string]*vconn{}
	nconns = map[string]*nconn{}
)

func uptimeRaw() float64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_UPTIME_RAW, &ts)
	return float64(ts.Sec) + float64(ts.Nsec)/1e9
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	path := os.Args[1]
	_ = os.Remove(path)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("hostrecv listening on %s, pid %d", path, os.Getpid())
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			log.Fatal(err)
		}
		go serve(c)
	}
}

func serve(c *net.UnixConn) {
	defer c.Close()
	buf := make([]byte, 65536)
	oob := make([]byte, unix.CmsgSpace(4*4))
	for {
		n, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
		if err != nil || n == 0 {
			return
		}
		fd := -1
		if oobn > 0 {
			msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
			if err == nil && len(msgs) > 0 {
				if fds, err := unix.ParseUnixRights(&msgs[0]); err == nil && len(fds) > 0 {
					fd = fds[0]
				}
			}
		}
		for _, line := range bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n")) {
			var r req
			out := map[string]any{}
			if err := json.Unmarshal(line, &r); err != nil {
				out["error"] = "bad request: " + err.Error()
			} else {
				out = handle(r, fd)
				fd = -1
			}
			out["op"] = r.Op
			out["id"] = r.ID
			b, _ := json.Marshal(out)
			log.Printf("%s -> %s", line, b)
			if _, err := c.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}
}

func handle(r req, fd int) map[string]any {
	switch r.Op {
	case "adopt-vsock":
		return adoptVsock(r, fd)
	case "check":
		return check(r)
	case "watch":
		return watch(r)
	case "watch-status":
		return watchStatus(r)
	case "guest-dial":
		return callJSON(r, "/x18.Guest/Dial", map[string]any{"port": r.Port})
	case "events":
		return callJSON(r, "/x18.Guest/Events", map[string]any{})
	case "adopt-net":
		return adoptNet(r, fd)
	case "net":
		return netEcho(r)
	case "close":
		return closeID(r.ID)
	}
	return map[string]any{"error": "unknown op"}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func adoptVsock(r req, fd int) map[string]any {
	if fd < 0 {
		return map[string]any{"error": "no descriptor attached"}
	}
	out := vs.Describe(fd)
	out["receivedFD"] = fd
	c, err := vs.FromFD(fd, "passed-"+r.ID)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	used := false
	var umu sync.Mutex
	cc, err := grpc.NewClient("passthrough:///"+r.ID,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(vs.Codec{})),
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{BaseDelay: 50 * time.Millisecond, MaxDelay: time.Second}, MinConnectTimeout: time.Second}),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			umu.Lock()
			defer umu.Unlock()
			if used {
				return nil, errors.New("passed descriptor already used; no redial")
			}
			used = true
			return c, nil
		}))
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	v := &vconn{conn: c, cc: cc}
	mu.Lock()
	vconns[r.ID] = v
	mu.Unlock()
	for k, val := range doCheck(v, r) {
		out[k] = val
	}
	if r.Once {
		closeID(r.ID)
	}
	return out
}

func doCheck(v *vconn, r req) map[string]any {
	out := map[string]any{}
	to := time.Duration(r.TimeoutMs) * time.Millisecond
	if to == 0 {
		to = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()
	t := time.Now()
	hr, err := healthpb.NewHealthClient(v.cc).Check(ctx, &healthpb.HealthCheckRequest{})
	out["healthMs"] = vs.Ms(t)
	out["healthAtUptimeRaw"] = uptimeRaw()
	if err != nil {
		out["ok"] = false
		out["healthError"] = err.Error()
		return out
	}
	out["health"] = hr.GetStatus().String()
	n := r.BulkBytes
	if n == 0 {
		n = 1024
	}
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	var back []byte
	t = time.Now()
	err = v.cc.Invoke(ctx, "/x18.Guest/Echo", &payload, &back, grpc.MaxCallRecvMsgSize(64<<20), grpc.MaxCallSendMsgSize(64<<20))
	out["echoMs"] = vs.Ms(t)
	out["echoBytes"] = n
	if err != nil {
		out["ok"] = false
		out["echoError"] = err.Error()
		return out
	}
	out["ok"] = bytes.Equal(back, payload)
	return out
}

func getV(id string) *vconn {
	mu.Lock()
	defer mu.Unlock()
	return vconns[id]
}

func check(r req) map[string]any {
	v := getV(r.ID)
	if v == nil {
		return map[string]any{"error": "no such id"}
	}
	out := doCheck(v, r)
	out["grpcState"] = v.cc.GetState().String()
	return out
}

func watch(r req) map[string]any {
	v := getV(r.ID)
	if v == nil {
		return map[string]any{"error": "no such id"}
	}
	st, err := healthpb.NewHealthClient(v.cc).Watch(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	go func() {
		for {
			m, err := st.Recv()
			v.mu.Lock()
			ts := time.Now().UTC().Format(time.RFC3339Nano)
			if err != nil {
				v.watch = append(v.watch, ts+" end: "+err.Error())
				v.mu.Unlock()
				return
			}
			v.watch = append(v.watch, ts+" status: "+m.GetStatus().String())
			v.mu.Unlock()
		}
	}()
	return map[string]any{"ok": true}
}

func watchStatus(r req) map[string]any {
	v := getV(r.ID)
	if v == nil {
		return map[string]any{"error": "no such id"}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return map[string]any{"watch": append([]string(nil), v.watch...), "grpcState": v.cc.GetState().String()}
}

func callJSON(r req, method string, in any) map[string]any {
	v := getV(r.ID)
	if v == nil {
		return map[string]any{"error": "no such id"}
	}
	b, _ := json.Marshal(in)
	var back []byte
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t := time.Now()
	err := v.cc.Invoke(ctx, method, &b, &back)
	out := map[string]any{"ms": vs.Ms(t)}
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	var res any
	if json.Unmarshal(back, &res) == nil {
		out["result"] = res
	} else {
		out["raw"] = string(back)
	}
	return out
}

func closeID(id string) map[string]any {
	mu.Lock()
	defer mu.Unlock()
	if v, ok := vconns[id]; ok {
		_ = v.cc.Close()
		_ = v.conn.Close()
		delete(vconns, id)
	}
	if n, ok := nconns[id]; ok {
		_ = n.f.Close()
		delete(nconns, id)
	}
	return map[string]any{"ok": true}
}

// --- network: ICMPv6 echo to ff02::1 through the file-handle attachment.

var (
	hostMAC  = []byte{0x02, 0x58, 0x02, 0x00, 0x00, 0x01}
	hostIP   = []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	allNodes = []byte{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
)

const echoID = 0x5818

func csum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

func ipv6Frame(dstMAC, src, dst, icmp []byte) []byte {
	msg := append([]byte(nil), icmp...)
	pseudo := append(append(append([]byte(nil), src...), dst...), 0, 0, byte(len(msg)>>8), byte(len(msg)), 0, 0, 0, 58)
	c := csum(append(pseudo, msg...))
	binary.BigEndian.PutUint16(msg[2:], c)
	f := append(append(append([]byte(nil), dstMAC...), hostMAC...), 0x86, 0xdd)
	f = append(f, 0x60, 0, 0, 0, byte(len(msg)>>8), byte(len(msg)), 58, 255)
	f = append(append(append(f, src...), dst...), msg...)
	return f
}

func echoRequest(seq uint16) []byte {
	icmp := []byte{128, 0, 0, 0, echoID >> 8, echoID & 0xff, byte(seq >> 8), byte(seq)}
	icmp = append(icmp, []byte(fmt.Sprintf("x18-echo-%d", seq))...)
	return ipv6Frame([]byte{0x33, 0x33, 0, 0, 0, 1}, hostIP, allNodes, icmp)
}

func neighborAdvert(f []byte) []byte {
	if len(f) < 78 || binary.BigEndian.Uint16(f[12:]) != 0x86dd || f[20] != 58 || f[54] != 135 {
		return nil
	}
	if !bytes.Equal(f[62:78], hostIP) {
		return nil
	}
	srcIP := f[22:38]
	if bytes.Equal(srcIP, make([]byte, 16)) {
		return nil
	}
	icmp := append(append([]byte{136, 0, 0, 0, 0x60, 0, 0, 0}, hostIP...), 2, 1)
	icmp = append(icmp, hostMAC...)
	return ipv6Frame(f[6:12], hostIP, srcIP, icmp)
}

func adoptNet(r req, fd int) map[string]any {
	if fd < 0 {
		return map[string]any{"error": "no descriptor attached"}
	}
	out := vs.Describe(fd)
	out["receivedFD"] = fd
	if err := unix.SetNonblock(fd, true); err != nil {
		out["error"] = err.Error()
		return out
	}
	n := &nconn{f: os.NewFile(uintptr(fd), "net-"+r.ID), fd: fd}
	mu.Lock()
	nconns[r.ID] = n
	mu.Unlock()
	go func() {
		b := make([]byte, 65536)
		for {
			k, err := n.f.Read(b)
			if err != nil {
				n.mu.Lock()
				n.rerr = err.Error()
				n.mu.Unlock()
				return
			}
			f := append([]byte(nil), b[:k]...)
			if na := neighborAdvert(f); na != nil {
				_, _ = n.f.Write(na)
			}
			rec := frameRec{t: time.Now(), seq: -1}
			if len(f) >= 62 && binary.BigEndian.Uint16(f[12:]) == 0x86dd && f[20] == 58 && f[54] == 129 && binary.BigEndian.Uint16(f[58:]) == echoID {
				rec.seq = int(binary.BigEndian.Uint16(f[60:]))
				rec.summary = "echo-reply"
			} else if len(f) >= 14 {
				rec.summary = fmt.Sprintf("eth/%04x", binary.BigEndian.Uint16(f[12:]))
			}
			n.mu.Lock()
			n.frames = append(n.frames, rec)
			n.mu.Unlock()
		}
	}()
	for k, v := range doEcho(n, r) {
		out[k] = v
	}
	if r.Once {
		closeID(r.ID)
	}
	return out
}

func doEcho(n *nconn, r req) map[string]any {
	wait := time.Duration(r.WaitMs) * time.Millisecond
	if wait == 0 {
		wait = 5 * time.Second
	}
	t := time.Now()
	deadline := t.Add(wait)
	var sent []uint16
	var werr error
	for time.Now().Before(deadline) {
		n.mu.Lock()
		n.seq++
		s := n.seq
		n.mu.Unlock()
		sent = append(sent, s)
		if _, err := n.f.Write(echoRequest(s)); err != nil {
			werr = err
		}
		until := time.Now().Add(20 * time.Millisecond)
		for time.Now().Before(until) {
			n.mu.Lock()
			for _, fr := range n.frames {
				for _, ss := range sent {
					if fr.seq == int(ss) {
						n.mu.Unlock()
						return map[string]any{"ok": true, "replyMs": float64(fr.t.Sub(t).Microseconds()) / 1000, "replyAtUptimeRaw": uptimeRaw() - time.Since(fr.t).Seconds(), "sent": len(sent), "lastWriteError": errStr(werr)}
					}
				}
			}
			rerr := n.rerr
			n.mu.Unlock()
			if rerr != "" {
				return map[string]any{"ok": false, "readError": rerr, "sent": len(sent), "lastWriteError": errStr(werr)}
			}
			time.Sleep(time.Millisecond)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return map[string]any{"ok": false, "error": "no echo reply", "sent": len(sent), "framesSeen": len(n.frames), "lastWriteError": errStr(werr), "readError": n.rerr}
}

func netEcho(r req) map[string]any {
	mu.Lock()
	n := nconns[r.ID]
	mu.Unlock()
	if n == nil {
		return map[string]any{"error": "no such id"}
	}
	return doEcho(n, r)
}
