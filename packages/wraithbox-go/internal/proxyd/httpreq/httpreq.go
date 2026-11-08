// Package httpreq holds what wb-proxyd does to each request on an
// inspected stream whatever its HTTP version: the request model, the
// strict reader of an HTTP/1.1 request head, the request form, the
// chunked body reader, the transfer coding rules, and the steps of
// S07-08-requests-and-credentials, "Order of the steps". The HTTP/1.1
// connection loop (package http1) and HTTP/2 (I499) are planned to
// serve requests with it.
//
// Rules match the path that goes upstream (SEC06-repo-writes), so the
// reader is strict where a lenient one would let a path or a body mean
// one thing here and another upstream: every head that isn't in the
// form RFC 9112 asks for is refused before any library normalizes it,
// with a rule a log reader can look up (NFR06-explained-refusals). The
// head is capped at HeadCap (SEC13-bounded-resources), and the
// transfer coding has one form, so a request can't be framed two ways
// (S07-08-requests-and-credentials, "Transfer coding").
//
// Every byte read here is the guest's, so each check fails closed
// (SEC05-default-deny). Nothing here logs: the caller logs each refusal
// with its rule (SEC10-audit). Audit mode relaxes none of these
// refusals (S07-16-tls-and-tunnels, "Not relaxed by audit mode or
// approvals"), so nothing here reads the mode. The package has no OS
// code.
package httpreq

import (
	"net/http"
	"strconv"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// The rules of this package's refusals (S07-08-requests-and-credentials;
// S07-16-tls-and-tunnels, "`CONNECT` and other tunnels").
const (
	// RuleRequestForm: a request line or header field outside the form
	// of RFC 9112, a target that isn't in origin or absolute form, or a
	// `Host` or target host that isn't the stream's name and port
	// (S07-08-requests-and-credentials, "Request form").
	RuleRequestForm = "http-request-form"
	// RulePathForm: a path that match.CanonicalPath refuses.
	RulePathForm = match.RulePathForm
	// RuleHeaderSize: a request head over HeadCap
	// (S07-08-requests-and-credentials, "Header cap").
	RuleHeaderSize = "http-header-size"
	// RuleTransferCoding: a `Transfer-Encoding` other than one
	// `chunked`, one with `Content-Length`, or a chunked body whose
	// framing fails to decode (S07-08-requests-and-credentials,
	// "Transfer coding").
	RuleTransferCoding = "http-transfer-coding"
	// RuleTunnel: a `CONNECT`, or an `Upgrade` to anything but
	// WebSocket (S07-16-tls-and-tunnels, "`CONNECT` and other tunnels").
	RuleTunnel = "http-tunnel"
	// RuleStepMissing: the credential removal step or the HTTP policy
	// step isn't provided, so no request may go upstream
	// (S07-08-requests-and-credentials, "Order of the steps").
	RuleStepMissing = "http-step-missing"
)

// HeadCap is the most bytes a request head may take: the request line,
// the header fields and the empty line that ends them, with their line
// ends (S07-08-requests-and-credentials, "Header cap"; S04-08-records-and-bounds).
const HeadCap = 64 << 10

// Refusal is a request that wb-proxyd answers itself, with Status, and
// never sends upstream.
type Refusal struct {
	Status int
	Rule   string
	// Detail says what failed. It holds offsets and names, never the
	// request's bytes.
	Detail string
	// Body, when set, is the whole answer's body, such as the HTTP
	// policy's (httppolicy.Decision.RefusalBody). Otherwise the answer
	// names the rule and the host.
	Body string
}

func (r *Refusal) Error() string {
	return r.Rule + ": " + strconv.Itoa(r.Status) + ": " + r.Detail
}

func refuse(status int, rule, detail string) *Refusal {
	return &Refusal{Status: status, Rule: rule, Detail: detail}
}

func formError(detail string) *Refusal {
	return refuse(http.StatusBadRequest, RuleRequestForm, detail)
}

// Binding is what a request must be bound to: the stream's name and
// port, and whether the guest's stream is TLS (S07-07-hosts-and-modes,
// "Name binding").
type Binding struct {
	Name dnsname.Name
	Port uint16
	TLS  bool
}

// Request is one request as the guest sent it, after its head passed
// the reader's checks, and as the steps change it.
type Request struct {
	Method string
	// Proto is "HTTP/1.1" or "HTTP/1.0".
	Proto string
	// Path is the path as received, without the query: the target's in
	// origin form, or the path of an absolute-form target, "/" when it
	// has none.
	Path string
	// RawQuery is the query as received, without its `?`. HasQuery
	// reports a `?`, so an empty query goes upstream as it came.
	RawQuery string
	HasQuery bool
	// CanonicalPath is Path in canonical form, which the canonicalize
	// step sets. It is the path every later step matches and the one
	// sent upstream (S07-08-requests-and-credentials, "Request form").
	CanonicalPath string
	// Header holds the header fields as received, under canonical
	// names, without the ones recorded in the fields below and the
	// hop-by-hop ones: `Host`, `Content-Length`, `Transfer-Encoding`,
	// `Upgrade`, `Expect`, `Connection` and each field it names,
	// `Keep-Alive`, `TE`, `Trailer` and `Proxy-Connection`. The steps
	// remove and add fields.
	Header http.Header
	// Binding is the stream's.
	Binding Binding
	// ContentLength is the `Content-Length`, or -1 without one.
	ContentLength int64
	// TransferEncoding holds the value of each `Transfer-Encoding`
	// field line. The transfer coding step checks it and sets Chunked.
	TransferEncoding []string
	Chunked          bool
	// WebSocket marks an `Upgrade: websocket` request.
	WebSocket bool
	// ExpectContinue marks an `Expect: 100-continue` request.
	ExpectContinue bool
	// Close reports that the guest asked to close the stream after the
	// response: `Connection: close`, or HTTP/1.0 without
	// `Connection: keep-alive`.
	Close bool
}

// HasBody reports whether the request has a body to send upstream. It
// reads Chunked, which the transfer coding step sets, so it holds only
// after Steps.Run accepted the request.
func (r *Request) HasBody() bool { return r.Chunked || r.ContentLength > 0 }
