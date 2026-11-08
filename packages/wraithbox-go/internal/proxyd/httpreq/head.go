package httpreq

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// headReader reads the lines of one request head, and counts the bytes
// it took from r against HeadCap.
type headReader struct {
	r *bufio.Reader
	n int
}

// line reads one line, ended by CRLF, and returns it without its end.
// It reads byte by byte, so it takes no byte of r past HeadCap. A bare
// LF is refused, and a CR elsewhere is left in the line, for the
// caller's check of its bytes to refuse.
func (h *headReader) line() (string, error) {
	var b []byte
	for {
		if h.n >= HeadCap {
			return "", refuse(http.StatusRequestHeaderFieldsTooLarge, RuleHeaderSize,
				"the request head is longer than "+strconv.Itoa(HeadCap)+" bytes")
		}
		c, err := h.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && h.n > 0 {
				err = io.ErrUnexpectedEOF
			}
			return "", err
		}
		h.n++
		if c != '\n' {
			b = append(b, c)
			continue
		}
		if len(b) == 0 || b[len(b)-1] != '\r' {
			return "", formError("a line of the request head ends in a bare LF at offset " + strconv.Itoa(h.n-1))
		}
		return string(b[:len(b)-1]), nil
	}
}

// ReadRequest reads one request head from r, and checks it against the
// form of RFC 9112 and the stream's binding
// (S07-08-requests-and-credentials, "Request form"). It takes at most
// HeadCap bytes from r, and leaves the body in r.
//
// It returns a *Refusal for a head it refuses, or the reader's error:
// io.EOF when r ends before the head's first byte, and
// io.ErrUnexpectedEOF after it. A `CONNECT` is refused with
// http-tunnel as soon as its request line is read.
func ReadRequest(r *bufio.Reader, b Binding) (*Request, error) {
	h := &headReader{r: r}
	line, err := h.line()
	if err != nil {
		return nil, err
	}
	req, ref := parseRequestLine(line, b)
	if ref != nil {
		return nil, ref
	}
	if err := h.fields(req); err != nil {
		return nil, err
	}
	if ref := checkFields(req); ref != nil {
		return nil, ref
	}
	return req, nil
}

// parseRequestLine checks the request line: a token method, one space,
// the target, one space, and HTTP/1.1 or HTTP/1.0.
func parseRequestLine(line string, b Binding) (*Request, *Refusal) {
	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return nil, formError("the request line is not a method, a target and a version, each after one space")
	}
	method, target, proto := parts[0], parts[1], parts[2]
	if !isToken(method) {
		return nil, formError("the method is not a token")
	}
	// A method is case-sensitive, but a server may compare it without
	// case, so any spelling of CONNECT is a tunnel
	// (S07-16-tls-and-tunnels, "`CONNECT` and other tunnels").
	if equalFold(method, http.MethodConnect) {
		return nil, refuse(http.StatusForbidden, RuleTunnel, "a CONNECT request")
	}
	if proto != "HTTP/1.1" && proto != "HTTP/1.0" {
		return nil, formError("the version is not HTTP/1.1 or HTTP/1.0")
	}
	req := &Request{Method: method, Proto: proto, Binding: b, Header: http.Header{}, ContentLength: -1}
	if ref := parseTarget(req, target); ref != nil {
		return nil, ref
	}
	return req, nil
}

// fields reads the header fields up to the empty line that ends them.
func (h *headReader) fields(req *Request) error {
	for {
		line, err := h.line()
		if err != nil {
			return err
		}
		if line == "" {
			return nil
		}
		name, value, ref := parseField(line)
		if ref != nil {
			return ref
		}
		req.Header[name] = append(req.Header[name], value)
	}
}

// parseField checks one field line, and returns its canonical name and
// its value without the whitespace around it.
func parseField(line string) (string, string, *Refusal) {
	if line[0] == ' ' || line[0] == '\t' {
		return "", "", formError("a header field line is folded onto the one before it")
	}
	name, value, ok := strings.Cut(line, ":")
	switch {
	case !ok:
		return "", "", formError("a header field line has no colon")
	case strings.TrimRight(name, " \t") != name:
		return "", "", formError("a header field name has whitespace before its colon")
	case !isToken(name):
		return "", "", formError("a header field name is not a token")
	}
	value = strings.Trim(value, " \t")
	for i := range len(value) {
		if c := value[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			// The name is the guest's, so the detail leaves it out.
			return "", "", formError("a header field value has a control byte at offset " + strconv.Itoa(i))
		}
	}
	return http.CanonicalHeaderKey(name), value, nil
}

// checkFields checks the fields the request form and the framing read:
// one `Host` bound to the stream, at most one `Content-Length` of
// digits, the `Upgrade` and the `Expect`. The transfer coding step
// checks `Transfer-Encoding`, which this records.
func checkFields(req *Request) *Refusal {
	hosts := req.Header.Values("Host")
	if len(hosts) != 1 {
		return formError("the request has " + strconv.Itoa(len(hosts)) + " Host fields, not one")
	}
	if ref := checkAuthority(hosts[0], req.Binding, "the Host field"); ref != nil {
		return ref
	}
	if ref := parseContentLength(req); ref != nil {
		return ref
	}
	req.TransferEncoding = req.Header.Values("Transfer-Encoding")
	if ref := parseUpgrade(req); ref != nil {
		return ref
	}
	if ref := parseExpect(req); ref != nil {
		return ref
	}
	req.Close = wantsClose(req)
	return nil
}

// maxLengthDigits keeps a Content-Length within an int64.
const maxLengthDigits = 18

// parseContentLength takes one Content-Length of 1 to 18 digits. Two
// fields, or one with a list, are refused even when the values agree:
// a request has one length, read one way (S07-08-requests-and-credentials,
// "Transfer coding").
func parseContentLength(req *Request) *Refusal {
	values := req.Header.Values("Content-Length")
	switch {
	case len(values) == 0:
		return nil
	case len(values) > 1:
		return formError("the request has more than one Content-Length field")
	}
	v := values[0]
	if v == "" || len(v) > maxLengthDigits || strings.Trim(v, "0123456789") != "" {
		return formError("the Content-Length is not 1 to 18 digits")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return formError("the Content-Length is not 1 to 18 digits")
	}
	req.ContentLength = n
	return nil
}

// parseUpgrade takes one `Upgrade: websocket`, and refuses any other
// upgrade as a tunnel (S07-16-tls-and-tunnels, "`CONNECT` and other
// tunnels").
func parseUpgrade(req *Request) *Refusal {
	values := req.Header.Values("Upgrade")
	switch {
	case len(values) == 0:
		return nil
	case len(values) == 1 && equalFold(values[0], "websocket"):
		req.WebSocket = true
		return nil
	}
	return refuse(http.StatusForbidden, RuleTunnel, "an Upgrade to a protocol other than WebSocket")
}

// parseExpect takes one `Expect: 100-continue`, the only expectation
// RFC 9110 defines. Any other is refused, rather than passed to a
// server that may read it another way.
func parseExpect(req *Request) *Refusal {
	values := req.Header.Values("Expect")
	switch {
	case len(values) == 0:
		return nil
	case len(values) == 1 && equalFold(values[0], "100-continue"):
		req.ExpectContinue = true
		return nil
	}
	return formError("the request has an Expect other than one 100-continue")
}

// wantsClose reports whether the guest asked to close after the
// response (RFC 9112, section 9.3).
func wantsClose(req *Request) bool {
	tokens := connectionTokens(req.Header)
	if req.Proto == "HTTP/1.0" {
		return !tokens["keep-alive"]
	}
	return tokens["close"]
}

// connectionTokens returns the lower-case tokens of every `Connection`
// field.
func connectionTokens(h http.Header) map[string]bool {
	out := map[string]bool{}
	for _, v := range h.Values("Connection") {
		for t := range strings.SplitSeq(v, ",") {
			if t = strings.ToLower(strings.Trim(t, " \t")); t != "" {
				out[t] = true
			}
		}
	}
	return out
}

// equalFold compares two strings without ASCII case. strings.EqualFold
// folds Unicode too, so it takes the Kelvin sign for a `k` and the long
// s for an `s`. A server may not.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// isToken reports whether s is an RFC 9110 token: one or more tchar.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}
