// Throwaway spike for X21-dep-gate-registries. Not held to project gates.
//
// An inspecting HTTPS proxy on localhost that maps registry downloads to
// package, version and publish time for npm, PyPI, the Go module proxy and
// crates.io, and applies a minimum age with one of two strategies:
//
//	refuse: pass metadata unchanged, refuse a too-young download (403).
//	filter: remove too-young versions from metadata, and still refuse a
//	        too-young download as a backstop.
//
// Optionally it looks each download up in OSV (single query per download,
// or coalesced into querybatch), with or without a cache.
package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mode       = flag.String("mode", "off", "off|refuse|filter")
	minAge     = flag.Duration("min-age", 7*24*time.Hour, "minimum age")
	addr       = flag.String("addr", "127.0.0.1:0", "listen address")
	caOut      = flag.String("ca-out", "", "write CA cert PEM here")
	logPath    = flag.String("log", "", "JSONL event log")
	osvMode    = flag.String("osv", "off", "off|single|batch")
	osvCache   = flag.String("osv-cache", "", "JSON file for a persistent OSV cache (empty: no cache)")
	nowFlag    = flag.String("now", "", "override now (RFC3339) for reproducibility")
	osvOverlap = flag.Bool("osv-overlap", false, "run the OSV lookup while the download runs")
)

var now time.Time

var mitmHosts = map[string]bool{
	"registry.npmjs.org":     true,
	"pypi.org":               true,
	"files.pythonhosted.org": true,
	"proxy.golang.org":       true,
	"sum.golang.org":         true,
	"index.crates.io":        true,
	"static.crates.io":       true,
	"crates.io":              true,
}

// ---------- event log ----------

type event struct {
	TS        string  `json:"ts"`
	Mode      string  `json:"mode"`
	Host      string  `json:"host"`
	Path      string  `json:"path"`
	Kind      string  `json:"kind"` // metadata|download|other|denied
	Eco       string  `json:"eco,omitempty"`
	Name      string  `json:"name,omitempty"`
	Version   string  `json:"version,omitempty"`
	Published string  `json:"published,omitempty"`
	AgeHours  float64 `json:"age_h,omitempty"`
	Decision  string  `json:"decision"`
	Rule      string  `json:"rule,omitempty"`
	Removed   int     `json:"removed,omitempty"`
	Kept      int     `json:"kept,omitempty"`
	OSVms     float64 `json:"osv_ms,omitempty"`
	Vulns     int     `json:"vulns,omitempty"`
	Status    int     `json:"status,omitempty"`
	Ms        float64 `json:"ms,omitempty"`
}

var (
	logMu  sync.Mutex
	logOut io.Writer = os.Stderr
)

func emit(e event) {
	e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	e.Mode = *mode
	b, _ := json.Marshal(e)
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Write(append(b, '\n'))
}

// ---------- CA ----------

var (
	caCert  *x509.Certificate
	caKey   *ecdsa.PrivateKey
	leafMu  sync.Mutex
	leaves  = map[string]*tls.Certificate{}
	leafKey *ecdsa.PrivateKey
)

func initCA() {
	var err error
	caKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	leafKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "X21 spike throwaway CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		PermittedDNSDomains:   keys(mitmHosts),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	must(err)
	caCert, err = x509.ParseCertificate(der)
	must(err)
	if *caOut != "" {
		must(os.WriteFile(*caOut, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	}
}

func leafFor(host string) (*tls.Certificate, error) {
	leafMu.Lock()
	defer leafMu.Unlock()
	if c, ok := leaves[host]; ok {
		return c, nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(23 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, caCert.Raw}, PrivateKey: leafKey}
	leaves[host] = c
	return c, nil
}

// ---------- proxy plumbing ----------

var upstream = &http.Client{
	Transport: &http.Transport{
		Proxy:               nil,
		MaxIdleConnsPerHost: 64,
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     30 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       120 * time.Second,
}

type oneConnListener struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.c })
	if c != nil {
		return &closeNotifyConn{Conn: c, done: l.done}, nil
	}
	<-l.done
	return nil, io.EOF
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.c.LocalAddr() }

type closeNotifyConn struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *closeNotifyConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

func handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != "443" || !mitmHosts[host] {
		emit(event{Host: r.Host, Kind: "denied", Decision: "deny", Rule: "host-not-in-spike-allowlist"})
		http.Error(w, "x21 spike: host not allowed", http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", 500)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	bufrw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
	bufrw.Flush()
	tconn := tls.Server(conn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.ServerName != "" && hello.ServerName != host {
				return nil, fmt.Errorf("SNI %q != CONNECT host %q", hello.ServerName, host)
			}
			return leafFor(host)
		},
		NextProtos: []string{"http/1.1"},
	})
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveMITM(host, w, r) }),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	l := &oneConnListener{c: tconn, done: make(chan struct{})}
	go srv.Serve(l)
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Accept-Encoding"}

func fetch(r *http.Request, host string, override http.Header) (*http.Response, error) {
	u := "https://" + host + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
	if err != nil {
		return nil, err
	}
	req.Header = r.Header.Clone()
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	for k, v := range override {
		req.Header[k] = v
	}
	return upstream.Do(req)
}

func getJSON(u string, accept string, v any) (http.Header, int, error) {
	req, _ := http.NewRequest("GET", u, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := upstream.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return resp.Header, resp.StatusCode, fmt.Errorf("GET %s: %d", u, resp.StatusCode)
	}
	return resp.Header, 200, json.NewDecoder(resp.Body).Decode(v)
}

func copyResp(w http.ResponseWriter, resp *http.Response, body []byte) {
	for k, v := range resp.Header {
		if k == "Content-Length" || k == "Content-Encoding" || k == "Transfer-Encoding" || k == "Connection" {
			continue
		}
		w.Header()[k] = v
	}
	if body != nil {
		w.Header().Del("Etag")
		w.Header().Del("Last-Modified")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func refuse(w http.ResponseWriter, e event, msg string) {
	e.Decision = "refuse"
	e.Status = 403
	emit(e)
	http.Error(w, "wraith box dependency gate (x21 spike): "+msg, http.StatusForbidden)
}

// followRedirects is for proxy.golang.org downloads, which answer with a 302
// to a signed storage.googleapis.com URL. Following it here keeps that host
// off the guest's allowlist.
var followRedirects = &http.Client{Transport: upstream.Transport, Timeout: 300 * time.Second}

func passThrough(host string, w http.ResponseWriter, r *http.Request, e event) {
	start := time.Now()
	var resp *http.Response
	var err error
	if host == "proxy.golang.org" && e.Kind == "download" {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, "https://"+host+r.URL.RequestURI(), nil)
		resp, err = followRedirects.Do(req)
	} else {
		resp, err = fetch(r, host, nil)
	}
	if err != nil {
		e.Decision = "error"
		e.Rule = err.Error()
		emit(e)
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	e.Status = resp.StatusCode
	if e.Decision == "" {
		e.Decision = "allow"
	}
	copyResp(w, resp, nil)
	e.Ms = float64(time.Since(start).Microseconds()) / 1000
	emit(e)
}

func tooYoung(t time.Time) bool { return now.Sub(t) < *minAge }

func serveMITM(host string, w http.ResponseWriter, r *http.Request) {
	switch host {
	case "registry.npmjs.org":
		npmHandle(host, w, r)
	case "pypi.org":
		pypiIndexHandle(host, w, r)
	case "files.pythonhosted.org":
		pypiFileHandle(host, w, r)
	case "proxy.golang.org":
		goHandle(host, w, r)
	case "index.crates.io":
		cratesIndexHandle(host, w, r)
	case "static.crates.io":
		cratesFileHandle(host, w, r)
	default:
		passThrough(host, w, r, event{Host: host, Path: r.URL.Path, Kind: "other"})
	}
}

// gateDownload applies the age check and OSV to a mapped download.
func gateDownload(host string, w http.ResponseWriter, r *http.Request, e event, pub time.Time, known bool) {
	e.Kind = "download"
	if !known {
		e.Rule = "publish-time-unknown"
		if *mode == "off" {
			e.Decision = "allow-would-refuse"
			passThrough(host, w, r, e)
			return
		}
		refuse(w, e, fmt.Sprintf("%s %s@%s: publish time unknown (fail closed)", e.Eco, e.Name, e.Version))
		return
	}
	e.Published = pub.UTC().Format(time.RFC3339)
	e.AgeHours = now.Sub(pub).Hours()
	if tooYoung(pub) {
		e.Rule = "min-age"
		if *mode == "off" {
			e.Decision = "allow-would-refuse"
		} else {
			refuse(w, e, fmt.Sprintf("%s %s@%s published %s, younger than %s", e.Eco, e.Name, e.Version, e.Published, *minAge))
			return
		}
	}
	if *osvMode != "off" && *osvOverlap {
		// Look up OSV while the download runs; hold the body until both are done.
		type res struct {
			n   int
			err error
		}
		ch := make(chan res, 1)
		t0 := time.Now()
		go func() { n, err := osvLookup(e.Eco, e.Name, e.Version); ch <- res{n, err} }()
		resp, err := fetch(r, host, nil)
		var body []byte
		if err == nil {
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		o := <-ch
		e.OSVms = float64(time.Since(t0).Microseconds()) / 1000
		e.Vulns = o.n
		if o.err != nil || err != nil {
			e.Rule = fmt.Sprintf("osv-or-fetch-error: %v %v", o.err, err)
			refuse(w, e, "OSV lookup or download failed (fail closed)")
			return
		}
		e.Decision, e.Status = "allow", resp.StatusCode
		emit(e)
		for k, v := range resp.Header {
			if k != "Content-Length" && k != "Content-Encoding" && k != "Transfer-Encoding" {
				w.Header()[k] = v
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}
	if *osvMode != "off" {
		t0 := time.Now()
		n, err := osvLookup(e.Eco, e.Name, e.Version)
		e.OSVms = float64(time.Since(t0).Microseconds()) / 1000
		e.Vulns = n
		if err != nil {
			e.Rule = "osv-error: " + err.Error()
			refuse(w, e, "OSV lookup failed (fail closed)")
			return
		}
	}
	passThrough(host, w, r, e)
}

// ---------- npm ----------

type npmTimes struct {
	once  sync.Once
	times map[string]time.Time
	err   error
}

var (
	npmMu    sync.Mutex
	npmCache = map[string]*npmTimes{}
)

func npmGetTimes(name string) (map[string]time.Time, error) {
	npmMu.Lock()
	t, ok := npmCache[name]
	if !ok {
		t = &npmTimes{}
		npmCache[name] = t
	}
	npmMu.Unlock()
	t.once.Do(func() {
		var doc struct {
			Time map[string]string `json:"time"`
		}
		esc := strings.Replace(url.PathEscape(name), "%40", "@", 1)
		_, _, err := getJSON("https://registry.npmjs.org/"+esc, "application/json", &doc)
		if err != nil {
			t.err = err
			return
		}
		t.times = map[string]time.Time{}
		for v, s := range doc.Time {
			if v == "created" || v == "modified" {
				continue
			}
			if ts, err := time.Parse(time.RFC3339, s); err == nil {
				t.times[v] = ts
			}
		}
	})
	return t.times, t.err
}

func npmHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	e := event{Host: host, Path: p, Eco: "npm"}
	if strings.HasPrefix(p, "/-/") || r.Method != http.MethodGet {
		e.Kind = "other"
		passThrough(host, w, r, e)
		return
	}
	if i := strings.Index(p, "/-/"); i > 0 && strings.HasSuffix(p, ".tgz") {
		name, _ := url.PathUnescape(strings.TrimPrefix(p[:i], "/"))
		file := path.Base(p)
		base := name
		if j := strings.LastIndex(name, "/"); j >= 0 {
			base = name[j+1:]
		}
		e.Name = name
		if !strings.HasPrefix(file, base+"-") {
			e.Rule = "tarball-name-mismatch"
			gateDownload(host, w, r, e, time.Time{}, false)
			return
		}
		e.Version = strings.TrimSuffix(strings.TrimPrefix(file, base+"-"), ".tgz")
		times, err := npmGetTimes(name)
		pub, ok := times[e.Version]
		if err != nil {
			ok = false
		}
		gateDownload(host, w, r, e, pub, ok)
		return
	}
	// packument
	name, _ := url.PathUnescape(strings.TrimPrefix(p, "/"))
	e.Name = name
	e.Kind = "metadata"
	if *mode != "filter" {
		passThrough(host, w, r, e)
		return
	}
	times, err := npmGetTimes(name)
	if err != nil {
		e.Rule = "packument-times: " + err.Error()
		refuse(w, e, "cannot read publish times (fail closed)")
		return
	}
	resp, err := fetch(r, host, nil)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.Status = resp.StatusCode
		e.Decision = "allow"
		emit(e)
		copyResp(w, resp, nil)
		return
	}
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		refuse(w, e, "bad packument")
		return
	}
	var versions map[string]json.RawMessage
	json.Unmarshal(doc["versions"], &versions)
	removed := map[string]bool{}
	for v := range versions {
		ts, ok := times[v]
		if !ok || tooYoung(ts) { // unknown time: remove (fail closed)
			removed[v] = true
			delete(versions, v)
		}
	}
	var tags map[string]string
	json.Unmarshal(doc["dist-tags"], &tags)
	for tag, v := range tags {
		if !removed[v] {
			continue
		}
		if tag == "latest" {
			best := ""
			for cand := range versions {
				if strings.Contains(cand, "-") {
					continue
				}
				if best == "" || semverLess(best, cand) {
					best = cand
				}
			}
			if best != "" {
				tags[tag] = best
				continue
			}
		}
		delete(tags, tag)
	}
	doc["versions"], _ = json.Marshal(versions)
	doc["dist-tags"], _ = json.Marshal(tags)
	if raw, ok := doc["time"]; ok {
		var tm map[string]string
		json.Unmarshal(raw, &tm)
		for v := range removed {
			delete(tm, v)
		}
		doc["time"], _ = json.Marshal(tm)
	}
	body, _ := json.Marshal(doc)
	e.Removed = len(removed)
	e.Kept = len(versions)
	e.Decision = "filter"
	e.Status = 200
	emit(e)
	copyResp(w, resp, body)
}

func semverLess(a, b string) bool {
	pa, pb := strings.Split(strings.SplitN(a, "+", 2)[0], "."), strings.Split(strings.SplitN(b, "+", 2)[0], ".")
	for i := 0; i < 3 && i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// ---------- PyPI ----------

type pyFile struct {
	name, version string
	uploaded      time.Time
}

var (
	pyMu    sync.Mutex
	pyFiles = map[string]pyFile{} // files.pythonhosted.org path -> file
)

func normalize(n string) string {
	n = strings.ToLower(n)
	r := strings.NewReplacer("_", "-", ".", "-")
	n = r.Replace(n)
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	return n
}

// pyParseFilename maps a distribution file name to project and version.
// Wheels are unambiguous; legacy sdists with '-' in the name are not.
func pyParseFilename(f string) (name, version string, ambiguous bool) {
	switch {
	case strings.HasSuffix(f, ".whl"):
		parts := strings.Split(strings.TrimSuffix(f, ".whl"), "-")
		if len(parts) >= 5 {
			return normalize(parts[0]), parts[1], false
		}
	case strings.HasSuffix(f, ".tar.gz"), strings.HasSuffix(f, ".zip"), strings.HasSuffix(f, ".tar.bz2"):
		stem := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(f, ".tar.gz"), ".zip"), ".tar.bz2")
		i := strings.LastIndex(stem, "-")
		if i > 0 {
			return normalize(stem[:i]), stem[i+1:], strings.Count(stem, "-") > 1
		}
	}
	return "", "", true
}

func pypiIndexHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	e := event{Host: host, Path: p, Eco: "pypi"}
	if !strings.HasPrefix(p, "/simple/") || r.Method != http.MethodGet {
		e.Kind = "other"
		passThrough(host, w, r, e)
		return
	}
	e.Kind = "metadata"
	e.Name = strings.Trim(strings.TrimPrefix(p, "/simple/"), "/")
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "application/vnd.pypi.simple.v1+json") {
		e.Rule = "html-index-requested:" + accept
		if *mode == "filter" {
			refuse(w, e, "only the JSON simple API is filtered")
			return
		}
		passThrough(host, w, r, e)
		return
	}
	resp, err := fetch(r, host, http.Header{"Accept": {"application/vnd.pypi.simple.v1+json"}})
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.Status = resp.StatusCode
		e.Decision = "allow"
		emit(e)
		copyResp(w, resp, nil)
		return
	}
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		refuse(w, e, "bad simple JSON")
		return
	}
	var files []map[string]any
	json.Unmarshal(doc["files"], &files)
	kept := files[:0]
	keptVersions := map[string]bool{}
	removed := 0
	for _, f := range files {
		fn, _ := f["filename"].(string)
		u, _ := f["url"].(string)
		ut, _ := f["upload-time"].(string)
		ts, terr := time.Parse(time.RFC3339Nano, ut)
		name, ver, _ := pyParseFilename(fn)
		if pu, err := url.Parse(u); err == nil && terr == nil {
			pyMu.Lock()
			pyFiles[pu.Path] = pyFile{name: name, version: ver, uploaded: ts}
			pyMu.Unlock()
		}
		if *mode == "filter" && (terr != nil || tooYoung(ts)) {
			removed++
			continue
		}
		kept = append(kept, f)
		keptVersions[ver] = true
	}
	if *mode != "filter" {
		// re-serialize unchanged content
		body, _ := json.Marshal(doc)
		e.Decision = "allow"
		e.Kept = len(files)
		e.Status = 200
		emit(e)
		copyResp(w, resp, body)
		return
	}
	doc["files"], _ = json.Marshal(kept)
	var versions []string
	if json.Unmarshal(doc["versions"], &versions) == nil {
		vs := versions[:0]
		for _, v := range versions {
			if keptVersions[v] {
				vs = append(vs, v)
			}
		}
		doc["versions"], _ = json.Marshal(vs)
	}
	body, _ := json.Marshal(doc)
	e.Removed = removed
	e.Kept = len(kept)
	e.Decision = "filter"
	e.Status = 200
	emit(e)
	resp.Header.Set("Content-Type", "application/vnd.pypi.simple.v1+json")
	copyResp(w, resp, body)
}

func pypiFileHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	e := event{Host: host, Path: p, Eco: "pypi"}
	file := path.Base(p)
	if strings.HasSuffix(file, ".metadata") { // PEP 658 metadata file
		e.Kind = "metadata"
		passThrough(host, w, r, e)
		return
	}
	pyMu.Lock()
	f, ok := pyFiles[p]
	pyMu.Unlock()
	if ok {
		e.Name, e.Version = f.name, f.version
		gateDownload(host, w, r, e, f.uploaded, true)
		return
	}
	// Not seen in an index response: parse the file name and ask the JSON API.
	name, ver, amb := pyParseFilename(file)
	e.Name, e.Version = name, ver
	if amb {
		e.Rule = "ambiguous-filename"
	}
	var doc struct {
		URLs []struct {
			Filename string `json:"filename"`
			Upload   string `json:"upload_time_iso_8601"`
		} `json:"urls"`
	}
	if name == "" {
		gateDownload(host, w, r, e, time.Time{}, false)
		return
	}
	if _, _, err := getJSON("https://pypi.org/pypi/"+name+"/"+ver+"/json", "", &doc); err != nil {
		gateDownload(host, w, r, e, time.Time{}, false)
		return
	}
	for _, u := range doc.URLs {
		if u.Filename == file {
			ts, err := time.Parse(time.RFC3339Nano, u.Upload)
			gateDownload(host, w, r, e, ts, err == nil)
			return
		}
	}
	gateDownload(host, w, r, e, time.Time{}, false)
}

// ---------- Go module proxy ----------

func goUnescape(s string) string {
	var b strings.Builder
	up := false
	for _, c := range s {
		if c == '!' {
			up = true
			continue
		}
		if up {
			b.WriteRune(c - 'a' + 'A')
			up = false
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

var (
	goMu    sync.Mutex
	goTimes = map[string]time.Time{}
)

func goInfoTime(escMod, escVer string) (time.Time, error) {
	k := escMod + "@" + escVer
	goMu.Lock()
	t, ok := goTimes[k]
	goMu.Unlock()
	if ok {
		return t, nil
	}
	var info struct{ Time time.Time }
	if _, _, err := getJSON("https://proxy.golang.org/"+escMod+"/@v/"+escVer+".info", "", &info); err != nil {
		return time.Time{}, err
	}
	goMu.Lock()
	goTimes[k] = info.Time
	goMu.Unlock()
	return info.Time, nil
}

func goHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	e := event{Host: host, Path: p, Eco: "Go"}
	i := strings.Index(p, "/@")
	if i < 0 {
		e.Kind = "other"
		passThrough(host, w, r, e)
		return
	}
	escMod := strings.TrimPrefix(p[:i], "/")
	e.Name = goUnescape(escMod)
	rest := p[i+1:]
	switch {
	case rest == "@v/list":
		e.Kind = "metadata"
		if *mode != "filter" {
			passThrough(host, w, r, e)
			return
		}
		resp, err := fetch(r, host, nil)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var keep []string
		var wg sync.WaitGroup
		var mu sync.Mutex
		lines := strings.Fields(string(body))
		for _, v := range lines {
			wg.Add(1)
			go func(v string) {
				defer wg.Done()
				t, err := goInfoTime(escMod, v)
				if err == nil && !tooYoung(t) {
					mu.Lock()
					keep = append(keep, v)
					mu.Unlock()
				}
			}(v)
		}
		wg.Wait()
		sort.Strings(keep)
		out := []byte(strings.Join(keep, "\n") + "\n")
		e.Removed = len(lines) - len(keep)
		e.Kept = len(keep)
		e.Decision = "filter"
		e.Status = resp.StatusCode
		emit(e)
		copyResp(w, resp, out)
	case rest == "@latest":
		e.Kind = "metadata"
		// Simplification: refuse a too-young @latest in filter mode; the go
		// command then falls back to @v/list.
		var info struct {
			Version string
			Time    time.Time
		}
		if _, _, err := getJSON("https://proxy.golang.org/"+escMod+"/@latest", "", &info); err == nil && *mode == "filter" && tooYoung(info.Time) {
			e.Version = info.Version
			e.Decision = "filter"
			e.Rule = "latest-too-young"
			emit(e)
			http.Error(w, "not found", 404)
			return
		}
		passThrough(host, w, r, e)
	case strings.HasPrefix(rest, "@v/"):
		file := strings.TrimPrefix(rest, "@v/")
		ext := path.Ext(file)
		escVer := strings.TrimSuffix(file, ext)
		e.Version = goUnescape(escVer)
		if ext == ".info" || ext == ".mod" {
			e.Kind = "metadata"
			passThrough(host, w, r, e)
			return
		}
		t, err := goInfoTime(escMod, escVer)
		gateDownload(host, w, r, e, t, err == nil)
	default:
		e.Kind = "other"
		passThrough(host, w, r, e)
	}
}

// ---------- crates.io ----------

var (
	crMu    sync.Mutex
	crTimes = map[string]map[string]time.Time{}
)

func cratesIndexPath(name string) string {
	n := strings.ToLower(name)
	switch len(n) {
	case 1:
		return "1/" + n
	case 2:
		return "2/" + n
	case 3:
		return "3/" + n[:1] + "/" + n
	}
	return n[:2] + "/" + n[2:4] + "/" + n
}

func cratesParseIndex(body []byte) (map[string]time.Time, []string) {
	times := map[string]time.Time{}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		lines = append(lines, line)
		var ent struct {
			Vers    string `json:"vers"`
			Pubtime string `json:"pubtime"`
		}
		if json.Unmarshal([]byte(line), &ent) == nil {
			if t, err := time.Parse(time.RFC3339, ent.Pubtime); err == nil {
				times[ent.Vers] = t
			}
		}
	}
	return times, lines
}

func cratesIndexHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	e := event{Host: host, Path: p, Eco: "crates.io", Kind: "metadata"}
	if p == "/config.json" || *mode != "filter" {
		passThrough(host, w, r, e)
		return
	}
	resp, err := fetch(r, host, nil)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		copyResp(w, resp, body)
		return
	}
	times, lines := cratesParseIndex(body)
	var out []string
	for _, l := range lines {
		var ent struct {
			Vers string `json:"vers"`
		}
		json.Unmarshal([]byte(l), &ent)
		t, ok := times[ent.Vers]
		if !ok || tooYoung(t) {
			e.Removed++
			continue
		}
		out = append(out, l)
	}
	e.Name = path.Base(p)
	e.Kept = len(out)
	e.Decision = "filter"
	e.Status = 200
	emit(e)
	copyResp(w, resp, []byte(strings.Join(out, "\n")+"\n"))
}

func cratesFileHandle(host string, w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path // /crates/<name>/<name>-<ver>.crate
	e := event{Host: host, Path: p, Eco: "crates.io"}
	// Two forms: /crates/<name>/<name>-<ver>.crate (the redirect target of
	// the crates.io API) and /crates/<name>/<ver>/download (cargo, from the
	// `dl` template in the index's config.json).
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(parts) == 4 && parts[0] == "crates" && parts[3] == "download":
		e.Name, e.Version = parts[1], parts[2]
	case len(parts) == 3 && parts[0] == "crates" && strings.HasSuffix(parts[2], ".crate") && strings.HasPrefix(parts[2], parts[1]+"-"):
		e.Name = parts[1]
		e.Version = strings.TrimSuffix(strings.TrimPrefix(parts[2], parts[1]+"-"), ".crate")
	default:
		gateDownload(host, w, r, e, time.Time{}, false)
		return
	}
	crMu.Lock()
	times, ok := crTimes[e.Name]
	crMu.Unlock()
	if !ok {
		req, _ := http.NewRequest("GET", "https://index.crates.io/"+cratesIndexPath(e.Name), nil)
		resp, err := upstream.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			times, _ = cratesParseIndex(body)
			crMu.Lock()
			crTimes[e.Name] = times
			crMu.Unlock()
		}
	}
	t, ok := times[e.Version]
	gateDownload(host, w, r, e, t, ok)
}

// ---------- OSV ----------

var (
	osvMu     sync.Mutex
	osvCached = map[string]int{}
	osvHTTP   = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64, ForceAttemptHTTP2: true}}
)

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version string `json:"version"`
}

func osvKey(eco, name, ver string) string { return eco + "|" + name + "|" + ver }

func osvLookup(eco, name, ver string) (int, error) {
	k := osvKey(eco, name, ver)
	if *osvCache != "" {
		osvMu.Lock()
		n, ok := osvCached[k]
		osvMu.Unlock()
		if ok {
			return n, nil
		}
	}
	var n int
	var err error
	if *osvMode == "batch" {
		n, err = batcher.lookup(eco, name, ver)
	} else {
		n, err = osvSingle(eco, name, ver)
	}
	if err == nil && *osvCache != "" {
		osvMu.Lock()
		osvCached[k] = n
		osvMu.Unlock()
	}
	return n, err
}

func osvEco(eco string) string {
	if eco == "pypi" {
		return "PyPI"
	}
	return eco
}

func osvSingle(eco, name, ver string) (int, error) {
	var q osvQuery
	q.Package.Name, q.Package.Ecosystem, q.Version = name, osvEco(eco), ver
	b, _ := json.Marshal(q)
	resp, err := osvHTTP.Post("https://api.osv.dev/v1/query", "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("osv status %d", resp.StatusCode)
	}
	var out struct {
		Vulns []json.RawMessage `json:"vulns"`
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return len(out.Vulns), err
}

type pending struct {
	q    osvQuery
	done chan struct{}
	n    int
	err  error
}

type osvBatcher struct {
	mu    sync.Mutex
	queue []*pending
	timer *time.Timer
}

var batcher = &osvBatcher{}

const batchWindow = 20 * time.Millisecond

func (b *osvBatcher) lookup(eco, name, ver string) (int, error) {
	p := &pending{done: make(chan struct{})}
	p.q.Package.Name, p.q.Package.Ecosystem, p.q.Version = name, osvEco(eco), ver
	b.mu.Lock()
	b.queue = append(b.queue, p)
	if b.timer == nil {
		b.timer = time.AfterFunc(batchWindow, b.flush)
	}
	b.mu.Unlock()
	<-p.done
	return p.n, p.err
}

func (b *osvBatcher) flush() {
	b.mu.Lock()
	q := b.queue
	b.queue = nil
	b.timer = nil
	b.mu.Unlock()
	for len(q) > 0 {
		chunk := q
		if len(chunk) > 1000 {
			chunk = q[:1000]
		}
		q = q[len(chunk):]
		var req struct {
			Queries []osvQuery `json:"queries"`
		}
		for _, p := range chunk {
			req.Queries = append(req.Queries, p.q)
		}
		body, _ := json.Marshal(req)
		resp, err := osvHTTP.Post("https://api.osv.dev/v1/querybatch", "application/json", bytes.NewReader(body))
		var out struct {
			Results []struct {
				Vulns []json.RawMessage `json:"vulns"`
			} `json:"results"`
		}
		if err == nil {
			if resp.StatusCode != 200 {
				err = fmt.Errorf("osv batch status %d", resp.StatusCode)
			} else {
				err = json.NewDecoder(resp.Body).Decode(&out)
			}
			resp.Body.Close()
		}
		for i, p := range chunk {
			if err != nil {
				p.err = err
			} else if i < len(out.Results) {
				p.n = len(out.Results[i].Vulns)
			} else {
				p.err = fmt.Errorf("short batch result")
			}
			close(p.done)
		}
	}
}

func loadOSVCache() {
	if *osvCache == "" {
		return
	}
	b, err := os.ReadFile(*osvCache)
	if err == nil {
		json.Unmarshal(b, &osvCached)
	}
}

func saveOSVCache() {
	if *osvCache == "" {
		return
	}
	osvMu.Lock()
	b, _ := json.Marshal(osvCached)
	osvMu.Unlock()
	os.WriteFile(*osvCache, b, 0o600)
}

// ---------- main ----------

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	flag.Parse()
	now = time.Now()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		must(err)
		now = t
	}
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		must(err)
		logOut = f
	}
	initCA()
	loadOSVCache()
	ln, err := net.Listen("tcp", *addr)
	must(err)
	fmt.Println(ln.Addr().String())
	os.Stdout.Sync()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				handleConnect(w, r)
				return
			}
			// Go on macOS verifies TLS with the system trust store and
			// ignores SSL_CERT_FILE, so the go command is pointed at
			// GOPROXY=http://<addr>/goproxy instead of CONNECT.
			if strings.HasPrefix(r.URL.Path, "/goproxy/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/goproxy")
				r.URL.RawPath = ""
				goHandle("proxy.golang.org", w, r)
				return
			}
			if r.URL.Path == "/x21/save-osv-cache" {
				saveOSVCache()
				w.WriteHeader(204)
				return
			}
			emit(event{Host: r.Host, Path: r.URL.Path, Kind: "denied", Decision: "deny", Rule: "plain-http"})
			http.Error(w, "x21 spike: CONNECT only", 403)
		}),
		ReadHeaderTimeout: 30 * time.Second,
	}
	must(srv.Serve(ln))
}
