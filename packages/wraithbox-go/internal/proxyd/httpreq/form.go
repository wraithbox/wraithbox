package httpreq

import (
	"strconv"
	"strings"
)

// parseTarget reads the request target in origin form or absolute form
// into req's path and query (S07-08-requests-and-credentials, "Request
// form"). An asterisk-form or authority-form target is refused. An
// absolute-form target must use the stream's scheme, and name the
// stream's host and port. Its userinfo is dropped. The path's bytes are
// left to the canonicalize step, which refuses them with
// http-path-form. The query must be visible ASCII without a `#`.
func parseTarget(req *Request, target string) *Refusal {
	path := target
	if !strings.HasPrefix(target, "/") {
		var ref *Refusal
		if path, ref = absoluteTarget(target, req.Binding); ref != nil {
			return ref
		}
	}
	path, query, hasQuery := strings.Cut(path, "?")
	for i := range len(query) {
		if c := query[i]; c <= 0x20 || c >= 0x7f || c == '#' {
			return formError("the query has a byte that is not visible ASCII, or a #, at offset " + strconv.Itoa(i))
		}
	}
	req.Path, req.RawQuery, req.HasQuery = path, query, hasQuery
	return nil
}

// absoluteTarget checks an absolute-form target, and returns its path
// and query, with "/" for an empty path.
func absoluteTarget(target string, b Binding) (string, *Refusal) {
	scheme, rest, ok := strings.Cut(target, "://")
	if !ok {
		return "", formError("the target is not in origin form or absolute form")
	}
	want := "http"
	if b.TLS {
		want = "https"
	}
	if !equalFold(scheme, want) {
		return "", formError("the target's scheme is not the stream's, " + want)
	}
	end := strings.IndexAny(rest, "/?")
	if end < 0 {
		end = len(rest)
	}
	authority, path := rest[:end], rest[end:]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		if !isUserinfo(authority[:at]) {
			return "", formError("the target's userinfo has a byte RFC 3986 doesn't allow there")
		}
		authority = authority[at+1:]
	}
	if ref := checkAuthority(authority, b, "the target's host"); ref != nil {
		return "", ref
	}
	if path == "" || path[0] == '?' {
		path = "/" + path
	}
	return path, nil
}

// checkAuthority checks that a `Host` value or a target's authority is
// the stream's name, compared without ASCII case, with no port or the
// stream's port (S07-07-hosts-and-modes, "Name binding";
// S07-08-requests-and-credentials, "Request form"). The name holds only
// lower-case letters, digits, `-` and `.`, so an authority with any
// other byte, an IP literal among them, differs from it.
func checkAuthority(authority string, b Binding, what string) *Refusal {
	host, port, hasPort := strings.Cut(authority, ":")
	if !equalFold(host, b.Name.String()) || b.Name.String() == "" {
		return formError(what + " is not the stream's name")
	}
	if !hasPort {
		return nil
	}
	if port == "" || len(port) > 5 || strings.Trim(port, "0123456789") != "" {
		return formError(what + " has a port that is not 1 to 5 digits")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n != int(b.Port) {
		return formError(what + " has a port that is not the stream's, " + strconv.Itoa(int(b.Port)))
	}
	return nil
}

// isUserinfo reports whether s holds only the bytes RFC 3986, section
// 3.2.1, allows in userinfo: unreserved, sub-delims, `:` and
// percent-encoding of two hexadecimal digits. It is dropped, but a
// userinfo another parser reads another way could move where it thinks
// the host starts.
func isUserinfo(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c == '%' && (i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2])) {
			return false
		}
		alnum := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
		if !alnum && strings.IndexByte("-._~!$&'()*+,;=:%", c) < 0 {
			return false
		}
	}
	return true
}
