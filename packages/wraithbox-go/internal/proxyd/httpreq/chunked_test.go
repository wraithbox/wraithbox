package httpreq

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestChunkedReader decodes a chunked body with an extension and a
// trailer, drops the trailer, and leaves the next request's bytes.
func TestChunkedReader(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("3;ext=1\r\nabc\r\nA\r\n0123456789\r\n0\r\nX-T: v\r\n\r\nNEXT"))
	got, err := io.ReadAll(NewChunkedReader(r))
	if err != nil || string(got) != "abc0123456789" {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "NEXT" {
		t.Fatalf("left %q, want NEXT", rest)
	}
}

// TestC02ChunkedFraming feeds framings that fail to decode, and sees
// each end the body with a 400 and http-transfer-coding
// (S07-08-requests-and-credentials, "Transfer coding").
func TestC02ChunkedFraming(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"bad chunk size", "zz\r\nabc\r\n0\r\n\r\n"},
		{"size with a sign", "+3\r\nabc\r\n0\r\n\r\n"},
		{"size with a space", "3 \r\nabc\r\n0\r\n\r\n"},
		{"size too long", "1000000000000000\r\n"},
		{"no size", "\r\nabc\r\n"},
		{"bare LF", "3\nabc\r\n0\r\n\r\n"},
		{"data longer than its size", "3\r\nabcd\r\n0\r\n\r\n"},
		{"ends inside a chunk", "5\r\nabc"},
		{"ends before the last chunk", "3\r\nabc\r\n"},
		{"control byte in an extension", "3;a\x01\r\nabc\r\n0\r\n\r\n"},
		{"bad trailer", "0\r\nX T: v\r\n\r\n"},
		{"trailers over the cap", "0\r\nX-T: " + strings.Repeat("a", maxTrailers) + "\r\n\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := NewChunkedReader(bufio.NewReader(strings.NewReader(tc.body)))
			_, err := io.ReadAll(cr)
			wantRefusal(t, err, http.StatusBadRequest, RuleTransferCoding)
			if _, again := cr.Read(make([]byte, 1)); !errors.Is(again, err) {
				t.Fatalf("a later read gave %v, want %v", again, err)
			}
		})
	}
}
