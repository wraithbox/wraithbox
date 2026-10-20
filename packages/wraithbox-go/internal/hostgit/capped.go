package hostgit

// CappedBuffer keeps the first limit bytes written to it and drops the
// rest, so git can't make the caller hold unbounded output
// (SEC13-bounded-resources). A write never fails, so git doesn't stop
// on a broken pipe before the caller sees that the output was too long.
type CappedBuffer struct {
	buf   []byte
	limit int
	over  bool
}

// NewCappedBuffer returns a buffer that keeps at most limit bytes.
func NewCappedBuffer(limit int) *CappedBuffer {
	return &CappedBuffer{limit: limit}
}

// Bytes returns what the buffer kept.
func (c *CappedBuffer) Bytes() []byte { return c.buf }

// Over reports whether more than limit bytes were written.
func (c *CappedBuffer) Over() bool { return c.over }

func (c *CappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - len(c.buf)
	if len(p) > room {
		c.over = true
		c.buf = append(c.buf, p[:room]...)
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}
