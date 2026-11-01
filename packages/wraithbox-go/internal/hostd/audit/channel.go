package audit

import (
	"fmt"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// The records of the event channel: what wb-netd and wb-proxyd send
// wb-hostd (S04-08-records-and-bounds, "Event records"; S09-11-audit,
// "Classes"). wb-hostd checked each record's form before it gets here.
// The guest's strings are escaped and cut again here, before the write.

// The bounds of the guest's strings in a record, as the event channel
// bounds them (internal/eventchan).
const (
	maxHost   = 253
	maxMethod = 16
	maxPath   = 1024
)

// Channel names the sender of a record of the event channel, and its
// sequence number there.
type Channel struct {
	Sender   string `json:"sender"`
	Sequence uint64 `json:"sequence"`
}

// RunCount is a run record's count: how many events of its rule the
// sender counted in a window, and whether the window was still open
// when the VM stopped.
type RunCount struct {
	Count         uint64 `json:"count"`
	WindowSeconds uint32 `json:"window_seconds"`
	Open          bool   `json:"open,omitempty"`
}

// OpenRun is a run open at a VM's stop, in the stop record
// (S09-11-audit, "Runs with a count").
type OpenRun struct {
	Sender        string `json:"sender"`
	ClassUID      int    `json:"class_uid"`
	Decision      string `json:"decision"`
	Rule          string `json:"rule"`
	Count         uint64 `json:"count"`
	WindowSeconds uint32 `json:"window_seconds"`
}

// From is where a record of the event channel came from: the VM, the
// sender and the record's sequence number.
type From struct {
	Slot       string
	Generation uint64
	Sender     string
	Sequence   uint64
}

// ChannelEvent is one event of the event channel. Host, Method, Path
// and Label come from the guest's traffic and label.
type ChannelEvent struct {
	ClassUID          int
	Decision, Rule    string
	Host              string
	Port              uint32
	Method, Path      string
	BytesIn, BytesOut uint64
	Label             GuestLabel
}

// ChannelRun is one run of the event channel.
type ChannelRun struct {
	ClassUID       int
	Decision, Rule string
	Count          uint64
	WindowSeconds  uint32
	Open           bool
}

// Endpoint is the OCSF network_endpoint object: the VM as the source,
// and the destination host and port.
type Endpoint struct {
	Name     string `json:"name,omitempty"`
	UID      string `json:"uid,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Port     uint32 `json:"port,omitempty"`
}

// Traffic is the OCSF network_traffic object.
type Traffic struct {
	BytesIn  uint64 `json:"bytes_in"`
	BytesOut uint64 `json:"bytes_out"`
}

// URL is the OCSF url object, with the path only.
type URL struct {
	Path string `json:"path"`
}

// HTTPRequest is the OCSF http_request object.
type HTTPRequest struct {
	HTTPMethod string `json:"http_method,omitempty"`
	URL        *URL   `json:"url,omitempty"`
}

// network holds what the Network Activity and HTTP Activity records
// share.
type network struct {
	header
	Count       uint64    `json:"count,omitempty"`
	SrcEndpoint Endpoint  `json:"src_endpoint"`
	DstEndpoint *Endpoint `json:"dst_endpoint,omitempty"`
	Traffic     *Traffic  `json:"traffic,omitempty"`
}

// NetworkActivity is a Network Activity (4001) record: a lookup or a
// connection.
type NetworkActivity struct {
	network
}

// HTTPActivity is an HTTP Activity (4002) record: an inspected request.
type HTTPActivity struct {
	network
	HTTPRequest HTTPRequest `json:"http_request"`
}

var (
	_ Event = (*NetworkActivity)(nil)
	_ Event = (*HTTPActivity)(nil)
)

// The activities of Network Activity: a refusal, and traffic of any
// other decision.
var (
	netRefuse  = activity{ClassNetworkActivity, categoryNetwork, 5, "Refuse"}
	netTraffic = activity{ClassNetworkActivity, categoryNetwork, 6, "Traffic"}
)

// httpActivity is the HTTP Activity activity of a request's method. A
// run, which has no method, is Unknown.
func httpActivity(method string) activity {
	ids := map[string]int{
		"CONNECT": 1, "DELETE": 2, "GET": 3, "HEAD": 4, "OPTIONS": 5,
		"POST": 6, "PUT": 7, "TRACE": 8, "PATCH": 9,
	}
	if method == "" {
		return activity{ClassHTTPActivity, categoryNetwork, 0, "Unknown"}
	}
	id, ok := ids[method]
	if !ok {
		return activity{ClassHTTPActivity, categoryNetwork, 99, "Other"}
	}
	name := method[:1] + strings.ToLower(method[1:])
	return activity{ClassHTTPActivity, categoryNetwork, id, name}
}

// refused reports whether a decision refused the work.
func refused(decision string) bool {
	return strings.HasPrefix(decision, "refuse") || strings.HasPrefix(decision, "deny")
}

func (f From) fields(decision, rule string) Fields {
	return Fields{
		Decision: decision, Rule: rule, VM: f.Slot, Generation: f.Generation,
		Channel: &Channel{Sender: f.Sender, Sequence: f.Sequence},
	}
}

func (f From) source() Endpoint {
	return Endpoint{Name: vmbundle.Name(f.Slot), UID: fmt.Sprintf("%s/%d", f.Slot, f.Generation)}
}

// EventFromChannel is the record of one event of the event channel: a
// Network Activity, HTTP Activity or Detection Finding record of the
// VM.
func EventFromChannel(from From, e ChannelEvent) Record {
	f := from.fields(e.Decision, e.Rule)
	label := GuestLabel{
		Project: guestLabel(e.Label.Project), Session: guestLabel(e.Label.Session),
		User: guestLabel(e.Label.User), Program: guestLabel(e.Label.Program),
	}
	if label != (GuestLabel{}) {
		f.GuestLabel = &label
	}
	if e.ClassUID == ClassDetectionFinding {
		return from.finding(e.Rule, f)
	}
	n := from.network(e.Decision, f)
	if host := guestText(e.Host, maxHost); host != "" {
		n.DstEndpoint = &Endpoint{Hostname: host, Port: e.Port}
	}
	if e.BytesIn > 0 || e.BytesOut > 0 {
		n.Traffic = &Traffic{BytesIn: e.BytesIn, BytesOut: e.BytesOut}
	}
	if e.ClassUID == ClassHTTPActivity {
		return from.http(n, guestText(e.Method, maxMethod), guestText(e.Path, maxPath))
	}
	return from.netActivity(n)
}

// RunFromChannel is the record of one run of the event channel: its
// class's record, with the count.
func RunFromChannel(from From, r ChannelRun) Record {
	f := from.fields(r.Decision, r.Rule)
	f.Run = &RunCount{Count: r.Count, WindowSeconds: r.WindowSeconds, Open: r.Open}
	if r.ClassUID == ClassDetectionFinding {
		return from.finding(r.Rule, f)
	}
	n := from.network(r.Decision, f)
	n.Count = r.Count
	if r.ClassUID == ClassHTTPActivity {
		return from.http(n, "", "")
	}
	return from.netActivity(n)
}

// WithOpenRuns is a VM's stop record with the runs open at the stop.
func WithOpenRuns(stop Record, runs []OpenRun) Record {
	if len(runs) > 0 {
		stop.Event.head().Unmapped.OpenRuns = runs
	}
	return stop
}

// network starts a Network Activity or HTTP Activity record, its
// activity set by the caller.
func (f From) network(decision string, fields Fields) network {
	n := network{header: newHeader(netTraffic, severityInformational, "", fields), SrcEndpoint: f.source()}
	n.StatusID = statusSuccess
	if refused(decision) {
		n.SeverityID, n.StatusID = severityLow, statusFailure
	}
	return n
}

func (f From) netActivity(n network) Record {
	a := netTraffic
	if refused(n.Unmapped.Decision) {
		a = netRefuse
	}
	n.setActivity(a, "network "+n.Unmapped.Decision)
	return Record{Slot: f.Slot, Generation: f.Generation, Event: &NetworkActivity{network: n}}
}

func (f From) http(n network, method, path string) Record {
	n.setActivity(httpActivity(method), "http "+n.Unmapped.Decision)
	req := HTTPRequest{HTTPMethod: method}
	if path != "" {
		req.URL = &URL{Path: path}
	}
	return Record{Slot: f.Slot, Generation: f.Generation, Event: &HTTPActivity{network: n, HTTPRequest: req}}
}

func (n *network) setActivity(a activity, msg string) {
	n.ActivityID, n.ActivityName, n.ClassUID, n.CategoryUID = a.id, a.name, a.class, a.category
	n.TypeUID = a.class*100 + a.id
	n.Message = msg
}

// finding is a sender's Detection Finding, of the VM.
func (f From) finding(rule string, fields Fields) Record {
	title := "network finding"
	return Record{Slot: f.Slot, Generation: f.Generation, Event: &Finding{
		header: newHeader(findingCreate, severityMedium, title, fields),
		Device: &Device{TypeID: deviceVirtual, Name: vmbundle.Name(f.Slot)},
		FindingInfo: FindingInfo{
			UID: fmt.Sprintf("%s/%d/%s/%d", f.Slot, f.Generation, f.Sender, f.Sequence), Title: title, Types: []string{rule},
		},
	}}
}
