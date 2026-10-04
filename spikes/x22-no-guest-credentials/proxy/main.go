// Throwaway spike for X22-no-guest-credentials. Not held to project gates.
//
// An inspecting HTTPS proxy on localhost that applies one credential rule to
// every request a client sends, and logs what each client sent and what the
// upstream answered. Rules (-mode):
//
//	observe:   forward every header unchanged (baseline).
//	strip:     remove every credential header and cookie (S07 today).
//	read-only: strip, except on GET and HEAD.
//	issued:    strip, except a bearer token that this proxy saw an upstream
//	           issue, in this run, to a token request the proxy had stripped
//	           of credentials; only on GET/HEAD to the host that named the
//	           token endpoint.
//	inject:    strip, then inject a host-side anonymous credential for
//	           configured bindings (ghcr.io /v2/homebrew/core/).
//
// Plain HTTP on the same port: /r/<host>/<path> is forwarded to
// https://<host>/<path> with the same rule, for clients that can be pointed
// at an HTTP base URL (GOPROXY) but ignore SSL_CERT_FILE on macOS.
//
// Header values are never logged: only the scheme and a hash prefix.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	mode       = flag.String("mode", "observe", "observe|strip|read-only|issued|inject")
	injectKind = flag.String("inject-kind", "static", "static (Bearer QQ==) | flow (proxy fetches an anonymous token)")
	addr       = flag.String("addr", "127.0.0.1:0", "listen address")
	caOut      = flag.String("ca-out", "", "write CA cert PEM here")
	logPath    = flag.String("log", "", "JSONL event log")
	label      = flag.String("label", "", "free-form label copied into each event")
)

// credHeaders are removed by every rule except observe. Canonical form.
var credHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie",
	"X-Api-Key", "Api-Key", "Private-Token", "X-Auth-Token",
	"X-Registry-Auth", "Job-Token", "X-Goog-Api-Key",
}

// ---------- event log ----------

type credSeen struct {
	Header   string `json:"header"`
	Scheme   string `json:"scheme,omitempty"`
	Hash     string `json:"hash"`
	Decision string `json:"decision"` // forward|strip|inject
	Why      string `json:"why,omitempty"`
}

type event struct {
	TS         string     `json:"ts"`
	Mode       string     `json:"mode"`
	Label      string     `json:"label,omitempty"`
	Host       string     `json:"host"`
	Method     string     `json:"method"`
	Path       string     `json:"path"`
	QueryKeys  []string   `json:"query_keys,omitempty"`
	UA         string     `json:"ua,omitempty"`
	Creds      []credSeen `json:"creds,omitempty"`
	OtherAuth  []string   `json:"other_auth_headers,omitempty"`
	Injected   string     `json:"injected,omitempty"`
	Status     int        `json:"status"`
	WWWAuth    string     `json:"www_auth,omitempty"`
	SetCookie  []string   `json:"set_cookie,omitempty"`
	Location   string     `json:"location,omitempty"`
	TokenIssue bool       `json:"token_issued,omitempty"`
	LFSHeaders []string   `json:"lfs_action_headers,omitempty"`
	Refused    string     `json:"refused,omitempty"`
	Err        string     `json:"err,omitempty"`
}

var (
	logMu  sync.Mutex
	logOut io.Writer = os.Stderr
)

func emit(e event) {
	e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	e.Mode = *mode
	e.Label = *label
	b, _ := json.Marshal(e)
	logMu.Lock()
	defer logMu.Unlock()
	logOut.Write(append(b, '\n'))
}

func hashOf(v string) string {
	s := sha256.Sum256([]byte(v))
	return hex.EncodeToString(s[:4])
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
		Subject:               pkix.Name{CommonName: "X22 spike throwaway CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(12 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
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
		NotAfter:     time.Now().Add(11 * time.Hour),
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
	Timeout:       300 * time.Second,
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
	if err != nil || port != "443" {
		http.Error(w, "x22 spike: only :443", http.StatusForbidden)
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
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(host, w, r) }),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	l := &oneConnListener{c: tconn, done: make(chan struct{})}
	go srv.Serve(l)
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Accept-Encoding"}

// ---------- the rules ----------

type issuedToken struct {
	host string // host whose 401 named the realm
}

var (
	stateMu sync.Mutex
	// realms announced in a WWW-Authenticate by host: realm URL -> announcing host
	realms = map[string]string{}
	// tokens issued by a realm to a stripped request: value hash -> where allowed
	issued = map[string]issuedToken{}
	// inject-kind=flow cache: scope -> token
	anonCache = map[string]string{}
)

func isRead(m string) bool { return m == http.MethodGet || m == http.MethodHead }

var bearerRe = regexp.MustCompile(`(?i)^bearer\s+(.+)$`)

func scheme(v string) string {
	if i := strings.IndexByte(v, ' '); i > 0 {
		return v[:i]
	}
	return ""
}

// applyRule edits req.Header in place and returns what it did.
func applyRule(host string, r *http.Request, req *http.Request, e *event) (refuse string) {
	if *mode == "observe" {
		for _, h := range credHeaders {
			for _, v := range r.Header.Values(h) {
				e.Creds = append(e.Creds, credSeen{Header: h, Scheme: scheme(v), Hash: hashOf(v), Decision: "forward"})
			}
		}
		return ""
	}
	for _, h := range credHeaders {
		vals := r.Header.Values(h)
		req.Header.Del(h)
		for _, v := range vals {
			c := credSeen{Header: h, Scheme: scheme(v), Hash: hashOf(v), Decision: "strip"}
			switch *mode {
			case "read-only":
				if isRead(r.Method) {
					c.Decision, c.Why = "forward", "read method"
					req.Header.Add(h, v)
				} else {
					c.Why = "write method"
				}
			case "issued":
				if h == "Authorization" {
					if m := bearerRe.FindStringSubmatch(v); m != nil {
						stateMu.Lock()
						t, ok := issued[hashOf(m[1])]
						stateMu.Unlock()
						switch {
						case !ok:
							c.Why = "not issued in this session"
						case t.host != host:
							c.Why = "issued for " + t.host
						case !isRead(r.Method):
							c.Why = "write method"
						default:
							c.Decision, c.Why = "forward", "issued by realm of "+host
							req.Header.Add(h, v)
						}
					}
				}
			}
			e.Creds = append(e.Creds, c)
		}
	}
	if *mode == "issued" {
		stateMu.Lock()
		_, isRealm := realms["https://"+host+r.URL.Path]
		stateMu.Unlock()
		if isRealm && r.Method != http.MethodGet {
			return "token endpoint: only GET without credentials (a POST body can carry a password or refresh token)"
		}
	}
	if *mode == "inject" && host == "ghcr.io" && strings.HasPrefix(r.URL.Path, "/v2/homebrew/core/") {
		if !isRead(r.Method) {
			return "anonymous binding is read-only"
		}
		switch *injectKind {
		case "static":
			req.Header.Set("Authorization", "Bearer QQ==")
			e.Injected = "ghcr-anon-static"
		case "flow":
			tok, err := anonToken(r.URL.Path)
			if err != nil {
				e.Err = "anon token: " + err.Error()
			} else {
				req.Header.Set("Authorization", "Bearer "+tok)
				e.Injected = "ghcr-anon-flow"
			}
		}
	}
	return ""
}

// anonToken runs the registry token flow itself, with no credentials, for
// repository:homebrew/core/<name>:pull.
var repoRe = regexp.MustCompile(`^/v2/(homebrew/core/[^/]+(?:/[^/]+)?)/(?:manifests|blobs|tags)/`)

func anonToken(p string) (string, error) {
	m := repoRe.FindStringSubmatch(p)
	if m == nil {
		return "", fmt.Errorf("no repository in %q", p)
	}
	scope := "repository:" + m[1] + ":pull"
	stateMu.Lock()
	if t, ok := anonCache[scope]; ok {
		stateMu.Unlock()
		return t, nil
	}
	stateMu.Unlock()
	u := "https://ghcr.io/token?service=ghcr.io&scope=" + url.QueryEscape(scope)
	resp, err := upstream.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Token == "" {
		return "", fmt.Errorf("token endpoint %d", resp.StatusCode)
	}
	stateMu.Lock()
	anonCache[scope] = body.Token
	stateMu.Unlock()
	return body.Token, nil
}

var realmRe = regexp.MustCompile(`realm="([^"]+)"`)

// authish finds other request headers that look like credentials, which the
// rules don't touch but the result should list.
var authish = regexp.MustCompile(`(?i)key|token|auth|secret|session|sig|cred`)

func isCredHeader(k string) bool {
	for _, h := range credHeaders {
		if strings.EqualFold(h, k) {
			return true
		}
	}
	return false
}

func serve(host string, w http.ResponseWriter, r *http.Request) {
	e := event{Host: host, Method: r.Method, Path: r.URL.Path, UA: r.Header.Get("User-Agent")}
	for k := range r.URL.Query() {
		e.QueryKeys = append(e.QueryKeys, k)
	}
	sort.Strings(e.QueryKeys)
	for k := range r.Header {
		if authish.MatchString(k) && !isCredHeader(k) {
			e.OtherAuth = append(e.OtherAuth, k)
		}
	}
	sort.Strings(e.OtherAuth)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, "https://"+host+r.URL.RequestURI(), r.Body)
	if err != nil {
		e.Err = err.Error()
		emit(e)
		http.Error(w, err.Error(), 400)
		return
	}
	req.ContentLength = r.ContentLength
	req.Header = r.Header.Clone()
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	if why := applyRule(host, r, req, &e); why != "" {
		e.Refused, e.Status = why, 403
		emit(e)
		http.Error(w, "x22 spike: refused: "+why, http.StatusForbidden)
		return
	}
	strippedAll := true
	for _, h := range credHeaders {
		if req.Header.Get(h) != "" {
			strippedAll = false
		}
	}

	resp, err := upstream.Do(req)
	if err != nil {
		e.Err = err.Error()
		emit(e)
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	e.Status = resp.StatusCode
	if wa := resp.Header.Get("Www-Authenticate"); wa != "" {
		e.WWWAuth = wa
		if m := realmRe.FindStringSubmatch(wa); m != nil {
			stateMu.Lock()
			realms[m[1]] = host
			stateMu.Unlock()
		}
	}
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if i := strings.IndexByte(sc, '='); i > 0 {
			e.SetCookie = append(e.SetCookie, sc[:i])
		}
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := url.Parse(loc); err == nil {
			ks := []string{}
			for k := range u.Query() {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			e.Location = u.Host + " " + strings.Join(ks, ",")
		}
	}

	// Bodies we need to read: token endpoint answers and LFS batch answers.
	var body []byte
	stateMu.Lock()
	announcer, isRealm := realms["https://"+host+r.URL.Path]
	stateMu.Unlock()
	isLFS := strings.HasSuffix(r.URL.Path, "/info/lfs/objects/batch")
	if (isRealm || isLFS) && resp.StatusCode == 200 {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if isRealm {
			var t struct {
				Token       string `json:"token"`
				AccessToken string `json:"access_token"`
			}
			if json.Unmarshal(body, &t) == nil {
				for _, v := range []string{t.Token, t.AccessToken} {
					if v == "" {
						continue
					}
					e.TokenIssue = true
					// Only a token issued to a request that carried no
					// credentials is anonymous by construction.
					if *mode == "issued" && strippedAll && r.Method == http.MethodGet {
						stateMu.Lock()
						issued[hashOf(v)] = issuedToken{host: announcer}
						stateMu.Unlock()
					}
				}
			}
		}
		if isLFS {
			var b struct {
				Objects []struct {
					Actions map[string]struct {
						Href   string            `json:"href"`
						Header map[string]string `json:"header"`
					} `json:"actions"`
				} `json:"objects"`
			}
			if json.Unmarshal(body, &b) == nil {
				seen := map[string]bool{}
				for _, o := range b.Objects {
					for name, a := range o.Actions {
						u, _ := url.Parse(a.Href)
						for hk, hv := range a.Header {
							k := name + " " + u.Host + " " + hk + " " + scheme(hv)
							if !seen[k] {
								seen[k] = true
								e.LFSHeaders = append(e.LFSHeaders, k)
							}
						}
						if len(a.Header) == 0 {
							k := name + " " + u.Host + " (no header)"
							if !seen[k] {
								seen[k] = true
								e.LFSHeaders = append(e.LFSHeaders, k)
							}
						}
					}
				}
			}
		}
	}
	emit(e)

	for k, v := range resp.Header {
		if k == "Content-Length" || k == "Transfer-Encoding" || k == "Connection" {
			continue
		}
		w.Header()[k] = v
	}
	if body != nil {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(resp.ContentLength))
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func main() {
	flag.Parse()
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		must(err)
		logOut = f
	}
	initCA()
	ln, err := net.Listen("tcp", *addr)
	must(err)
	fmt.Println(ln.Addr().String())
	os.Stdout.Sync()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			handleConnect(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/r/") {
			rest := strings.TrimPrefix(r.URL.Path, "/r/")
			i := strings.IndexByte(rest, '/')
			if i < 0 {
				http.Error(w, "bad", 400)
				return
			}
			host := rest[:i]
			r.URL.Path = rest[i:]
			r.URL.RawPath = ""
			serve(host, w, r)
			return
		}
		http.Error(w, "x22 spike: CONNECT or /r/<host>/ only", 400)
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	must(srv.Serve(ln))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

var _ = bytes.NewReader
