package httpreq

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// Bounds of the chunked reader (SEC13-bounded-resources).
const (
	// maxChunkLine is the longest chunk size line, extensions included.
	maxChunkLine = 4 << 10
	// maxSizeDigits keeps a chunk size within an int64.
	maxSizeDigits = 15
	// maxTrailers is the most bytes the trailer section may take. It
	// is read and dropped: no trailer goes upstream
	// (S07-08-requests-and-credentials, "Request form").
	maxTrailers = HeadCap
)

// chunkedReader decodes a chunked body (RFC 9112, section 7.1).
type chunkedReader struct {
	r *bufio.Reader
	// left is what is left of the current chunk's data.
	left int64
	// started is set after the first chunk size line, so each later one
	// is read after its chunk's CRLF.
	started bool
	err     error
}

// NewChunkedReader returns a reader of the decoded data of a chunked
// body in r. It reads strictly: a size of 1 to 15 hexadecimal digits
// with no whitespace, an extension of visible ASCII, spaces and tabs
// after a `;`, CRLF line ends and nothing else after each chunk's data.
// It reads the trailer section, and drops it. Every framing it refuses,
// and an end of r before the last chunk, gives a *Refusal with
// http-transfer-coding, from that read and every later one
// (S07-08-requests-and-credentials, "Transfer coding"). After the
// trailer section it gives io.EOF, and leaves the next request's bytes
// in r.
func NewChunkedReader(r *bufio.Reader) io.Reader {
	return &chunkedReader{r: r}
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if c.left == 0 {
		if c.err = c.next(); c.err != nil {
			return 0, c.err
		}
	}
	n, err := c.r.Read(p[:min(int64(len(p)), c.left)])
	c.left -= int64(n)
	if err != nil {
		c.err = framing(err, "the body ended inside a chunk")
		return n, c.err
	}
	return n, nil
}

// next reads the CRLF after the last chunk's data, and the next chunk
// size line. After the last chunk it reads the trailer section and
// returns io.EOF.
func (c *chunkedReader) next() error {
	if c.started {
		line, err := chunkLine(c.r, 0)
		if err != nil {
			return err
		}
		if line != "" {
			return chunkError("a chunk's data is not followed by CRLF")
		}
	}
	c.started = true
	line, err := chunkLine(c.r, maxChunkLine)
	if err != nil {
		return err
	}
	size, err := parseChunkSize(line)
	if err != nil {
		return err
	}
	if size > 0 {
		c.left = size
		return nil
	}
	if err := dropTrailers(c.r); err != nil {
		return err
	}
	return io.EOF
}

// parseChunkSize reads a chunk size line: hexadecimal digits, then
// nothing or a `;` and an extension, which is dropped.
func parseChunkSize(line string) (int64, error) {
	digits := 0
	for digits < len(line) && isHex(line[digits]) {
		digits++
	}
	if digits == 0 || digits > maxSizeDigits {
		return 0, chunkError("a chunk size is not 1 to 15 hexadecimal digits")
	}
	if rest := line[digits:]; rest != "" {
		if rest[0] != ';' {
			return 0, chunkError("a chunk size is followed by a byte other than ; or CRLF")
		}
		for i := 1; i < len(rest); i++ {
			if c := rest[i]; (c < 0x20 && c != '\t') || c >= 0x7f {
				return 0, chunkError("a chunk extension has a byte that is not visible ASCII, a space or a tab")
			}
		}
	}
	size, err := strconv.ParseInt(line[:digits], 16, 64)
	if err != nil {
		return 0, chunkError("a chunk size is not 1 to 15 hexadecimal digits")
	}
	return size, nil
}

// dropTrailers reads the trailer section up to its empty line, within
// maxTrailers, and checks each field line as a header field.
func dropTrailers(r *bufio.Reader) error {
	// left counts every byte of the section, the CRLF of each line and
	// of the empty line at its end among them.
	left := maxTrailers
	for {
		line, err := chunkLine(r, left-2)
		if err != nil {
			return err
		}
		if line == "" {
			return nil
		}
		left -= len(line) + 2
		if _, _, ref := parseField(line); ref != nil {
			return chunkError("a trailer field line is not in the form of a header field")
		}
	}
}

// chunkLine reads one line ended by CRLF, of at most limit bytes before
// its end. A limit of 0 reads only an empty line, and a negative one no
// line.
func chunkLine(r *bufio.Reader, limit int) (string, error) {
	var b []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", framing(err, "the body ended inside a chunk's framing")
		}
		if c == '\n' {
			break
		}
		if len(b) > limit {
			return "", chunkError("a line of the chunked framing is too long, or a chunk is longer than its size")
		}
		b = append(b, c)
	}
	if len(b) == 0 || b[len(b)-1] != '\r' {
		return "", chunkError("a line of the chunked framing ends in a bare LF")
	}
	return string(b[:len(b)-1]), nil
}

// framing turns a read error inside the body into a refusal: an end of
// the stream inside the framing is a framing that fails to decode.
// Another error, such as the stream's reset, is returned as it is.
func framing(err error, detail string) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return chunkError(detail)
	}
	return err
}

func chunkError(detail string) *Refusal {
	return refuse(http.StatusBadRequest, RuleTransferCoding, detail)
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
