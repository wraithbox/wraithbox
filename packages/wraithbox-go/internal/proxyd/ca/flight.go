package ca

import "crypto/tls"

// flight is one leaf being signed. The calls of For that ask for the
// same leaf while it is signed wait for it, so parallel streams to a new
// hostname make one key and one signature
// (SEC13-bounded-resources).
type flight struct {
	name string
	// from is the cache the flight was started in.
	from *leafCache
	// done is closed once leaf or ref is set.
	done chan struct{}
	leaf *tls.Certificate
	ref  *Refusal
}

// join returns the flight of host's leaf, and true when the caller
// starts it and signs. The caller holds Authority.mu.
func (c *leafCache) join(host string) (*flight, bool) {
	if f, ok := c.flights[host]; ok {
		return f, false
	}
	f := &flight{name: host, from: c, done: make(chan struct{})}
	c.flights[host] = f
	return f, true
}

// leave removes a flight that has ended. The caller holds
// Authority.mu.
func (c *leafCache) leave(f *flight) {
	if c.flights[f.name] == f {
		delete(c.flights, f.name)
	}
}
