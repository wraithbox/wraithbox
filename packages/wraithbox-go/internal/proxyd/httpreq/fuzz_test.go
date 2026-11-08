package httpreq

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// chunks gives data in reads whose sizes come from sizes, in turn, so
// the reader sees a head cut at arbitrary places.
type chunks struct {
	data  []byte
	sizes []byte
	i     int
	read  int
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if len(c.sizes) > 0 {
		n = min(n, int(c.sizes[c.i%len(c.sizes)])+1)
		c.i++
	}
	n = copy(p, c.data[:min(n, len(c.data))])
	c.data = c.data[n:]
	c.read += n
	return n, nil
}

// FuzzHTTP1RequestHead feeds the head reader arbitrary bytes in
// arbitrary chunks. It takes no byte past HeadCap, and a head it
// accepts has one Host bound to the stream, a token method, and a
// transfer coding that the step accepts or refuses with its rule
// (S07-08-requests-and-credentials, "Request form", "Header cap",
// "Transfer coding").
func FuzzHTTP1RequestHead(f *testing.F) {
	f.Add([]byte("GET /a?b HTTP/1.1\r\nHost: example.com\r\n\r\n"), []byte{3, 7})
	f.Add([]byte("POST https://u@example.com:443/x HTTP/1.1\r\nHost: example.com:443\r\nTransfer-Encoding: chunked\r\n\r\n"), []byte{0})
	f.Add([]byte("GET / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 3\r\nUpgrade: websocket\r\n\r\nabc"), []byte{})
	f.Add([]byte("CONNECT example.com:443 HTTP/1.1\r\n\r\n"), []byte{1})
	f.Fuzz(func(t *testing.T, data, sizes []byte) {
		src := &chunks{data: data, sizes: sizes}
		r := bufio.NewReader(src)
		req, err := ReadRequest(r, tlsBinding(t))
		if taken := src.read - r.Buffered(); taken > HeadCap {
			t.Fatalf("the reader took %d bytes, past the cap", taken)
		}
		if err != nil {
			var ref *Refusal
			if !errors.As(err, &ref) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("an error that is not a refusal or an EOF: %v", err)
			}
			return
		}
		checkAcceptedHead(t, req)
		checkAgainstNetHTTP(t, req, data[:src.read-r.Buffered()])
	})
}

// checkAcceptedHead checks the properties FuzzHTTP1RequestHead asks of
// a head the reader accepted.
func checkAcceptedHead(t *testing.T, req *Request) {
	t.Helper()
	for _, name := range recorded {
		if req.Header.Values(name) != nil {
			t.Fatalf("an accepted head's Header holds %s", name)
		}
	}
	if !isToken(req.Method) || strings.EqualFold(req.Method, http.MethodConnect) {
		t.Fatalf("an accepted head has method %q", req.Method)
	}
	ref := TransferCoding(req)
	if ref != nil && ref.Rule != RuleTransferCoding {
		t.Fatalf("TransferCoding refused with %s", ref.Rule)
	}
	if ref == nil && req.Chunked != (len(req.TransferEncoding) == 1) {
		t.Fatalf("an accepted transfer coding with Chunked %v", req.Chunked)
	}
	if ref == nil && req.Chunked && req.ContentLength >= 0 {
		t.Fatal("an accepted head is chunked and has a Content-Length")
	}
}

// FuzzCanonicalPath feeds the canonicalize step arbitrary paths. A path
// it accepts starts with `/`, holds no dot segment, literal or encoded,
// no control byte and no backslash, and canonicalizing it again changes
// nothing (S07-08-requests-and-credentials, "Request form").
func FuzzCanonicalPath(f *testing.F) {
	for _, s := range []string{"/a/./b/../c", "/@scope%2fname", "/%2e%2e", "/wget%2F..%2Fcurl", "/a;b/..;/c", "//a//b", "/%41%7e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		req := &Request{Path: path}
		if ref := Canonicalize(req); ref != nil {
			if ref.Rule != RulePathForm || ref.Status != http.StatusBadRequest {
				t.Fatalf("refused with %d %s", ref.Status, ref.Rule)
			}
			return
		}
		checkCanonical(t, req.CanonicalPath)
	})
}

// checkCanonical checks the properties FuzzCanonicalPath asks of a
// canonical path.
func checkCanonical(t *testing.T, canon string) {
	t.Helper()
	if !strings.HasPrefix(canon, "/") || strings.ContainsAny(canon, "\\") {
		t.Fatalf("canonical path %q", canon)
	}
	for i := range len(canon) {
		if c := canon[i]; c < 0x20 || c >= 0x7f {
			t.Fatalf("canonical path %q has byte %#x", canon, c)
		}
	}
	if hasDotSegment(canon) {
		t.Fatalf("canonical path %q has a dot segment", canon)
	}
	again, ref := match.CanonicalPath(canon)
	if ref != nil || again != canon {
		t.Fatalf("canonicalizing %q again gave %q, %v", canon, again, ref)
	}
}

// hasDotSegment reports whether a path has a `.` or `..` component,
// literal or percent-encoded, in a segment split on `/` and `%2F`, or
// before a `;`.
func hasDotSegment(path string) bool {
	decoded := strings.ReplaceAll(strings.ToLower(path), "%2e", ".")
	for part := range strings.SplitSeq(strings.ReplaceAll(decoded, "%2f", "/"), "/") {
		part, _, _ = strings.Cut(part, ";")
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

// checkAgainstNetHTTP parses a head the reader accepted, and whose path
// and transfer coding the steps accept, with net/http as a reference.
// Both must read the same method, Host, length and transfer coding.
func checkAgainstNetHTTP(t *testing.T, req *Request, head []byte) {
	t.Helper()
	if TransferCoding(req) != nil || Canonicalize(req) != nil {
		return
	}
	ref, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
	if err != nil {
		t.Fatalf("net/http refuses an accepted head: %v", err)
	}
	if ref.Method != req.Method {
		t.Fatalf("method %q, net/http %q", req.Method, ref.Method)
	}
	if checkAuthority(ref.Host, req.Binding, "Host") != nil {
		t.Fatalf("net/http reads Host %q, not the stream's", ref.Host)
	}
	if req.Chunked != (len(ref.TransferEncoding) == 1 && ref.TransferEncoding[0] == "chunked") {
		t.Fatalf("chunked %v, net/http %q", req.Chunked, ref.TransferEncoding)
	}
	if !req.Chunked && max(req.ContentLength, 0) != ref.ContentLength {
		t.Fatalf("length %d, net/http %d", req.ContentLength, ref.ContentLength)
	}
}

// FuzzChunkedReader feeds the chunked reader arbitrary bytes in
// arbitrary reads. Every error is a refusal with http-transfer-coding,
// and it sticks. A body it accepts, httputil.NewChunkedReader decodes
// to the same data, and the bytes left start right after the trailer
// section (S07-08-requests-and-credentials, "Transfer coding").
func FuzzChunkedReader(f *testing.F) {
	f.Add([]byte("3;ext=1\r\nabc\r\nA\r\n0123456789\r\n0\r\nX-T: v\r\n\r\nNEXT"), []byte{2, 5})
	f.Add([]byte("zz\r\nabc\r\n0\r\n\r\n"), []byte{})
	f.Add([]byte("0\r\n\r\n"), []byte{0})
	f.Fuzz(func(t *testing.T, data, sizes []byte) {
		r := bufio.NewReader(&chunks{data: data, sizes: sizes})
		cr := NewChunkedReader(r)
		got, err := io.ReadAll(cr)
		if err != nil {
			checkChunkedError(t, cr, err)
			return
		}
		rest, _ := io.ReadAll(r)
		body := data[:len(data)-len(rest)]
		want, refErr := io.ReadAll(httputil.NewChunkedReader(bufio.NewReader(bytes.NewReader(body))))
		if refErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("decoded %q, httputil %q, %v", got, want, refErr)
		}
		if !bytes.HasSuffix(body, []byte("\r\n\r\n")) {
			t.Fatalf("the body read ends in %q, not the end of a trailer section", body[max(0, len(body)-8):])
		}
	})
}

// checkChunkedError checks that err is a framing refusal, and that a
// later read gives it again.
func checkChunkedError(t *testing.T, cr io.Reader, err error) {
	t.Helper()
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Rule != RuleTransferCoding || ref.Status != http.StatusBadRequest {
		t.Fatalf("an error that is not a framing refusal: %v", err)
	}
	if _, again := cr.Read(make([]byte, 1)); !errors.Is(again, err) {
		t.Fatalf("a later read gave %v, want %v", again, err)
	}
}
