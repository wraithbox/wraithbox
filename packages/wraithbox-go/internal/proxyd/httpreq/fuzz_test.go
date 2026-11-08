package httpreq

import (
	"bufio"
	"errors"
	"io"
	"net/http"
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
	})
}

// checkAcceptedHead checks the properties FuzzHTTP1RequestHead asks of
// a head the reader accepted.
func checkAcceptedHead(t *testing.T, req *Request) {
	t.Helper()
	hosts := req.Header.Values("Host")
	if len(hosts) != 1 || checkAuthority(hosts[0], req.Binding, "Host") != nil {
		t.Fatalf("an accepted head has Host fields %q", hosts)
	}
	if !isToken(req.Method) || strings.EqualFold(req.Method, http.MethodConnect) {
		t.Fatalf("an accepted head has method %q", req.Method)
	}
	ref := TransferCoding(req)
	if ref != nil && ref.Rule != RuleTransferCoding {
		t.Fatalf("TransferCoding refused with %s", ref.Rule)
	}
	if ref == nil && req.Chunked != (len(req.Header.Values("Transfer-Encoding")) == 1) {
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
