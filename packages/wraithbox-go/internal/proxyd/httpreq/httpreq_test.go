package httpreq

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/dnsname"
)

func binding(t testing.TB, host string, port uint16, isTLS bool) Binding {
	t.Helper()
	name, ref := dnsname.FromHost(host)
	if ref != nil {
		t.Fatalf("FromHost(%q): %v", host, ref)
	}
	return Binding{Name: name, Port: port, TLS: isTLS}
}

func tlsBinding(t testing.TB) Binding { return binding(t, "example.com", 443, true) }

// read reads one head from s on the TLS binding of example.com:443.
func read(t *testing.T, s string) (*Request, error) {
	t.Helper()
	return ReadRequest(bufio.NewReader(strings.NewReader(s)), tlsBinding(t))
}

// wantRefusal checks that err is a *Refusal with status and rule.
func wantRefusal(t *testing.T, err error, status int, rule string) {
	t.Helper()
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want a refusal with %s", err, rule)
	}
	if ref.Status != status || ref.Rule != rule {
		t.Fatalf("refusal = %d %s (%s), want %d %s", ref.Status, ref.Rule, ref.Detail, status, rule)
	}
}

// mustRead reads one head that the reader accepts.
func mustRead(t *testing.T, s string) *Request {
	t.Helper()
	req, err := read(t, s)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	return req
}

// recorder is a step that records its calls, and refuses when told to.
type recorder struct {
	name   string
	calls  *[]string
	refuse *Refusal
}

func (r recorder) Apply(*Request) *Refusal {
	*r.calls = append(*r.calls, r.name)
	return r.refuse
}

// steps returns Steps whose required steps record into calls.
func steps(calls *[]string) *Steps {
	return &Steps{
		RemoveCredentials: recorder{name: "remove", calls: calls},
		Policy:            recorder{name: "policy", calls: calls},
	}
}

const host = "Host: example.com\r\n"

// TestC02RequestHeadRefusals feeds the reader each head decision 2 of
// I498 refuses, and sees each refused with its status and rule
// (S07-08-requests-and-credentials, "Request form").
func TestC02RequestHeadRefusals(t *testing.T) {
	cases := []struct {
		name, head string
	}{
		{"method not a token", "G(T / HTTP/1.1\r\n" + host + "\r\n"},
		{"version 2.0", "GET / HTTP/2.0\r\n" + host + "\r\n"},
		{"version lower case", "GET / http/1.1\r\n" + host + "\r\n"},
		{"two spaces", "GET  / HTTP/1.1\r\n" + host + "\r\n"},
		{"asterisk form", "OPTIONS * HTTP/1.1\r\n" + host + "\r\n"},
		{"authority form", "GET example.com:443 HTTP/1.1\r\n" + host + "\r\n"},
		{"bare LF on the request line", "GET / HTTP/1.1\n" + host + "\r\n"},
		{"bare LF on a field", "GET / HTTP/1.1\r\nHost: example.com\n\r\n"},
		{"folded field", "GET / HTTP/1.1\r\n" + host + "X-A: 1\r\n  2\r\n\r\n"},
		{"whitespace before colon", "GET / HTTP/1.1\r\nHost : example.com\r\n\r\n"},
		{"field name not a token", "GET / HTTP/1.1\r\n" + host + "X(A: 1\r\n\r\n"},
		{"field with no colon", "GET / HTTP/1.1\r\n" + host + "X-A\r\n\r\n"},
		{"control byte in a value", "GET / HTTP/1.1\r\n" + host + "X-A: a\x01b\r\n\r\n"},
		{"CR in a value", "GET / HTTP/1.1\r\n" + host + "X-A: a\rb\r\n\r\n"},
		{"no Host", "GET / HTTP/1.1\r\n\r\n"},
		{"two Host", "GET / HTTP/1.1\r\n" + host + host + "\r\n"},
		{"Host of another name", "GET / HTTP/1.1\r\nHost: example.org\r\n\r\n"},
		{"Host of another port", "GET / HTTP/1.1\r\nHost: example.com:8443\r\n\r\n"},
		{"Host with an empty port", "GET / HTTP/1.1\r\nHost: example.com:\r\n\r\n"},
		{"Host with userinfo", "GET / HTTP/1.1\r\nHost: u@example.com\r\n\r\n"},
		{"Host with a trailing dot", "GET / HTTP/1.1\r\nHost: example.com.\r\n\r\n"},
		{"Content-Length not digits", "POST / HTTP/1.1\r\n" + host + "Content-Length: 1a\r\n\r\n"},
		{"Content-Length signed", "POST / HTTP/1.1\r\n" + host + "Content-Length: +1\r\n\r\n"},
		{"Content-Length list", "POST / HTTP/1.1\r\n" + host + "Content-Length: 1, 1\r\n\r\n"},
		{"Content-Length differing", "POST / HTTP/1.1\r\n" + host + "Content-Length: 1\r\nContent-Length: 2\r\n\r\n"},
		{"Content-Length too long", "POST / HTTP/1.1\r\n" + host + "Content-Length: 9999999999999999999\r\n\r\n"},
		{"absolute form of another host", "GET https://example.org/ HTTP/1.1\r\n" + host + "\r\n"},
		{"absolute form of another scheme", "GET http://example.com/ HTTP/1.1\r\n" + host + "\r\n"},
		{"query with a space byte", "GET /?a\x7f HTTP/1.1\r\n" + host + "\r\n"},
		{"query with a fragment", "GET /?a#b HTTP/1.1\r\n" + host + "\r\n"},
		{"Expect other than 100-continue", "GET / HTTP/1.1\r\n" + host + "Expect: 200-ok\r\n\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := read(t, tc.head)
			wantRefusal(t, err, http.StatusBadRequest, RuleRequestForm)
		})
	}
}

// TestC02HeadOverCap sees a head over 64 KiB refused with 431 and
// http-header-size, and the reader take no byte past the cap.
func TestC02HeadOverCap(t *testing.T) {
	head := "GET / HTTP/1.1\r\n" + host + "X-Pad: " + strings.Repeat("a", HeadCap) + "\r\n\r\nNEXT"
	r := bufio.NewReader(strings.NewReader(head))
	_, err := ReadRequest(r, tlsBinding(t))
	wantRefusal(t, err, http.StatusRequestHeaderFieldsTooLarge, RuleHeaderSize)
	rest, _ := io.ReadAll(r)
	if taken := len(head) - len(rest); taken != HeadCap {
		t.Fatalf("the reader took %d bytes, want %d", taken, HeadCap)
	}
}

// TestHeadAtCap sees a head of exactly HeadCap bytes accepted.
func TestHeadAtCap(t *testing.T) {
	prefix := "GET / HTTP/1.1\r\n" + host + "X-Pad: "
	pad := HeadCap - len(prefix) - len("\r\n\r\n")
	if _, err := read(t, prefix+strings.Repeat("a", pad)+"\r\n\r\n"); err != nil {
		t.Fatalf("a head of HeadCap bytes: %v", err)
	}
}

// TestReadEOF sees an empty stream give io.EOF, and one that ends
// inside a head give io.ErrUnexpectedEOF.
func TestReadEOF(t *testing.T) {
	if _, err := read(t, ""); !errors.Is(err, io.EOF) {
		t.Fatalf("empty: err = %v, want io.EOF", err)
	}
	if _, err := read(t, "GET / HTTP/1.1\r\nHo"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("cut: err = %v, want io.ErrUnexpectedEOF", err)
	}
}

// TestC02RequestFormAccepted reads heads the request form accepts, and
// checks the request model.
func TestC02RequestFormAccepted(t *testing.T) {
	cases := []struct {
		name, head, path, query string
		hasQuery, close         bool
	}{
		{"origin form", "GET /a?b=c HTTP/1.1\r\n" + host + "\r\n", "/a", "b=c", true, false},
		{"empty query", "GET /a? HTTP/1.1\r\n" + host + "\r\n", "/a", "", true, false},
		{"Host with the stream's port", "GET / HTTP/1.1\r\nHost: example.com:443\r\n\r\n", "/", "", false, false},
		{"Host in upper case", "GET / HTTP/1.1\r\nHost: EXAMPLE.com\r\n\r\n", "/", "", false, false},
		{"absolute form with userinfo", "GET https://user:pw@example.com:443/x?y HTTP/1.1\r\n" + host + "\r\n", "/x", "y", true, false},
		{"absolute form with no path", "GET https://example.com?q HTTP/1.1\r\n" + host + "\r\n", "/", "q", true, false},
		{"HTTP/1.0 closes", "GET / HTTP/1.0\r\n" + host + "\r\n", "/", "", false, true},
		{"Connection: close", "GET / HTTP/1.1\r\n" + host + "Connection: keep-alive, Close\r\n\r\n", "/", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := read(t, tc.head)
			if err != nil {
				t.Fatalf("ReadRequest: %v", err)
			}
			if req.Path != tc.path || req.RawQuery != tc.query || req.HasQuery != tc.hasQuery || req.Close != tc.close {
				t.Fatalf("got path %q query %q (%v) close %v", req.Path, req.RawQuery, req.HasQuery, req.Close)
			}
		})
	}
}

// TestHostPortPlainHTTP sees `Host` with port 80 pass on a plain HTTP
// stream on port 80, and an absolute-form http target too.
func TestHostPortPlainHTTP(t *testing.T) {
	b := binding(t, "example.com", 80, false)
	for _, head := range []string{
		"GET / HTTP/1.1\r\nHost: example.com:80\r\n\r\n",
		"GET http://example.com/ HTTP/1.1\r\n" + host + "\r\n",
	} {
		if _, err := ReadRequest(bufio.NewReader(strings.NewReader(head)), b); err != nil {
			t.Fatalf("%q: %v", head, err)
		}
	}
}

// TestC02CanonicalPath runs the canonicalize step on request targets
// (S07-08-requests-and-credentials, "Request form", "Encoded slashes").
func TestC02CanonicalPath(t *testing.T) {
	cases := []struct {
		target, want string
	}{
		{"/a/./b/../c", "/a/c"},
		{"/@scope%2fname", "/@scope%2Fname"},
		{"/api/v4/projects/group%2Fproject", "/api/v4/projects/group%2Fproject"},
		{"/%2e%2e", ""},
		{"/wget%2F..%2Fcurl", ""},
		{"/a/../../b", ""},
		{"/a%zz", ""},
		{"/a%", ""},
		{"/a\\b", ""},
		{"/a\xc3\xa9", ""},
		{"/a#b", ""},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			req := mustRead(t, "GET "+tc.target+" HTTP/1.1\r\n"+host+"\r\n")
			ref := Canonicalize(req)
			if tc.want == "" {
				if ref == nil || ref.Status != http.StatusBadRequest || ref.Rule != RulePathForm {
					t.Fatalf("Canonicalize = %v (%q), want a 400 with %s", ref, req.CanonicalPath, RulePathForm)
				}
				return
			}
			if ref != nil || req.CanonicalPath != tc.want {
				t.Fatalf("Canonicalize = %v, %q, want %q", ref, req.CanonicalPath, tc.want)
			}
		})
	}
}

// TestC02TransferCoding sends each framing that S07-08-requests-and-credentials,
// "Transfer coding", refuses, and sees a 400 with http-transfer-coding
// before the HTTP policy step, so nothing goes upstream.
func TestC02TransferCoding(t *testing.T) {
	cases := []struct {
		name, fields string
	}{
		{"gzip, chunked", "Transfer-Encoding: gzip, chunked\r\n"},
		{"chunked, chunked", "Transfer-Encoding: chunked, chunked\r\n"},
		{"identity", "Transfer-Encoding: identity\r\n"},
		{"two lines", "Transfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n"},
		{"with Content-Length", "Transfer-Encoding: chunked\r\nContent-Length: 3\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mustRead(t, "POST / HTTP/1.1\r\n"+host+tc.fields+"\r\n")
			var calls []string
			ref := steps(&calls).Run(req)
			if ref == nil || ref.Status != http.StatusBadRequest || ref.Rule != RuleTransferCoding {
				t.Fatalf("Run = %v, want a 400 with %s", ref, RuleTransferCoding)
			}
			if strings.Join(calls, ",") != "remove" {
				t.Fatalf("steps called: %v, want only remove", calls)
			}
		})
	}
}

// TestTransferCodingOn checks the transfer coding of an HTTP/1.0
// request, refused, and of one chunked, accepted.
func TestTransferCodingOn(t *testing.T) {
	t.Run("HTTP/1.0", func(t *testing.T) {
		req, err := read(t, "POST / HTTP/1.0\r\n"+host+"Transfer-Encoding: chunked\r\n\r\n")
		if err != nil {
			t.Fatalf("ReadRequest: %v", err)
		}
		wantRefusal(t, TransferCoding(req), http.StatusBadRequest, RuleTransferCoding)
	})
	t.Run("one chunked", func(t *testing.T) {
		req, err := read(t, "POST / HTTP/1.1\r\n"+host+"Transfer-Encoding: Chunked\r\n\r\n")
		if err != nil {
			t.Fatalf("ReadRequest: %v", err)
		}
		if ref := TransferCoding(req); ref != nil || !req.Chunked || !req.HasBody() {
			t.Fatalf("TransferCoding = %v, chunked %v", ref, req.Chunked)
		}
	})
}

// TestC02Tunnels sees `CONNECT` and an `Upgrade` other than WebSocket
// refused with a 403 and http-tunnel by the reader, so no step, the
// HTTP policy among them, sees them (S07-16-tls-and-tunnels, "`CONNECT`
// and other tunnels").
func TestC02Tunnels(t *testing.T) {
	for _, head := range []string{
		"CONNECT example.com:443 HTTP/1.1\r\n" + host + "\r\n",
		"connect example.com:443 HTTP/1.1\r\n" + host + "\r\n",
		"GET / HTTP/1.1\r\n" + host + "Upgrade: h2c\r\n\r\n",
		"GET / HTTP/1.1\r\n" + host + "Upgrade: websocket, h2c\r\n\r\n",
		"GET / HTTP/1.1\r\n" + host + "Upgrade: websocket\r\nUpgrade: websocket\r\n\r\n",
	} {
		_, err := read(t, head)
		wantRefusal(t, err, http.StatusForbidden, RuleTunnel)
	}
	req, err := read(t, "GET / HTTP/1.1\r\n"+host+"Upgrade: WebSocket\r\nConnection: Upgrade\r\n\r\n")
	if err != nil || !req.WebSocket {
		t.Fatalf("a WebSocket upgrade: %v, WebSocket %v", err, req != nil && req.WebSocket)
	}
}

// TestC02StepMissing sees every request refused with http-step-missing
// when the credential removal step or the HTTP policy step is missing,
// before any step runs (S07-08-requests-and-credentials, "Order of the
// steps").
func TestC02StepMissing(t *testing.T) {
	var calls []string
	noRemove := steps(&calls)
	noRemove.RemoveCredentials = nil
	noPolicy := steps(&calls)
	noPolicy.Policy = nil
	for name, s := range map[string]*Steps{"nil": nil, "no removal": noRemove, "no policy": noPolicy} {
		req, err := read(t, "GET / HTTP/1.1\r\n"+host+"\r\n")
		if err != nil {
			t.Fatalf("ReadRequest: %v", err)
		}
		wantRefusal(t, s.Run(req), http.StatusInternalServerError, RuleStepMissing)
		if len(calls) != 0 || req.CanonicalPath != "" {
			t.Fatalf("%s: steps ran: %v, canonical path %q", name, calls, req.CanonicalPath)
		}
	}
}

// TestStepsOrder sees the steps run in the order of
// S07-08-requests-and-credentials, "Order of the steps", and stop at the
// first refusal.
func TestStepsOrder(t *testing.T) {
	var calls []string
	rec := func(name string) recorder { return recorder{name: name, calls: &calls} }
	s := &Steps{
		RemoveCredentials: rec("remove"),
		HeadChecks:        []Step{rec("head1"), rec("head2")},
		Policy:            rec("policy"),
		DependencyGate:    rec("gate"),
		Inject:            rec("inject"),
	}
	req, err := read(t, "GET /a/./b HTTP/1.1\r\n"+host+"\r\n")
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if ref := s.Run(req); ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	if got := strings.Join(calls, ","); got != "remove,head1,head2,policy,gate,inject" || req.CanonicalPath != "/a/b" {
		t.Fatalf("calls %s, canonical path %q", got, req.CanonicalPath)
	}

	calls = nil
	s.Policy = recorder{name: "policy", calls: &calls, refuse: refuse(http.StatusForbidden, "x", "")}
	if ref := s.Run(req); ref == nil || ref.Rule != "x" {
		t.Fatalf("Run = %v, want the policy's refusal", ref)
	}
	if got := strings.Join(calls, ","); got != "remove,head1,head2,policy" {
		t.Fatalf("calls %s after a refusal", got)
	}

	calls = nil
	bad, err := read(t, "GET /%2e%2e HTTP/1.1\r\n"+host+"\r\n")
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	wantRefusal(t, s.Run(bad), http.StatusBadRequest, RulePathForm)
	if len(calls) != 0 {
		t.Fatalf("steps ran after the canonicalize step refused: %v", calls)
	}
}

// TestRefusalHoldsNoGuestBytes sees a refusal's text leave out the
// guest's bytes (NFR06-explained-refusals).
func TestRefusalHoldsNoGuestBytes(t *testing.T) {
	const secret = "s3cr3t"
	for _, head := range []string{
		"GET / HTTP/1.1\r\nHost: " + secret + ".example.com\r\n\r\n",
		"GET / HTTP/1.1\r\n" + host + "X-" + secret + ": a\x01\r\n\r\n",
		"GET /" + secret + "%zz HTTP/1.1\r\n" + host + "\r\n",
	} {
		req, err := read(t, head)
		if err == nil {
			if ref := Canonicalize(req); ref != nil {
				err = ref
			}
		}
		if err == nil || strings.Contains(strings.ToLower(err.Error()), secret) {
			t.Fatalf("refusal %v for %q", err, head)
		}
	}
}
