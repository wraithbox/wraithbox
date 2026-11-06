package ca

import (
	"crypto/tls"
	"time"
)

// leafCache is a VM's leaves of one signing CA, by hostname. Each VM has
// its own, which a new signing CA or a new generation replaces, so it
// is keyed on VM, CA and hostname (S09-07-ca-certificates, "Leaf
// issuance"). The caller holds Authority.mu.
type leafCache struct {
	caID   string
	byHost map[string]*leafEntry
	// flights are the leaves being signed, by hostname.
	flights map[string]*flight
	// tick counts the uses, so the entry with the lowest used is the
	// least recently used.
	tick uint64
}

type leafEntry struct {
	leaf *tls.Certificate
	used uint64
}

func newLeafCache(caID string) *leafCache {
	return &leafCache{caID: caID, byHost: map[string]*leafEntry{}, flights: map[string]*flight{}}
}

// get returns the cached leaf for host, or nil when there is none or it
// ends within leafRenew, which drops it.
func (c *leafCache) get(host string, now time.Time) *tls.Certificate {
	e, ok := c.byHost[host]
	if !ok {
		return nil
	}
	if !now.Before(e.leaf.Leaf.NotAfter.Add(-leafRenew)) {
		delete(c.byHost, host)
		return nil
	}
	c.tick++
	e.used = c.tick
	return e.leaf
}

// put caches a leaf for host, unless a usable one is cached already.
// Past maxLeavesPerVM it drops the least recently used leaf, since the
// guest picks the hostnames (SEC13-bounded-resources).
func (c *leafCache) put(host string, leaf *tls.Certificate, now time.Time) {
	if c.get(host, now) != nil {
		return
	}
	c.tick++
	c.byHost[host] = &leafEntry{leaf: leaf, used: c.tick}
	if len(c.byHost) > maxLeavesPerVM {
		c.dropOldest()
	}
}

// dropOldest drops the least recently used leaf.
func (c *leafCache) dropOldest() {
	var oldest string
	var least uint64
	for host, e := range c.byHost {
		if oldest == "" || e.used < least {
			oldest, least = host, e.used
		}
	}
	delete(c.byHost, oldest)
}
