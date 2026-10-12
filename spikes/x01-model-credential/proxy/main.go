// Command x01-proxy is the throwaway TLS-inspecting proxy of spike
// X01-model-credential. It terminates every CONNECT with a leaf signed by
// a CA it generates at start, logs each host and request it sees, and on
// the hosts named by -inject replaces the placeholder API key with the
// real one. The real key never appears in a log line.
//
// Throwaway code: not held to the project gates, never merged.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type proxy struct {
	caCert      *x509.Certificate
	caKey       *ecdsa.PrivateKey
	placeholder string
	realKey     string
	inject      map[string]bool
	upstream    *tls.Config

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18080", "address to listen on")
	caOut := flag.String("ca-out", "x01-ca.pem", "where to write the CA certificate (PEM)")
	placeholder := flag.String("placeholder", "", "placeholder key the client sends")
	keySource := flag.String("key-source", "keychain:wraithbox-secret", "keychain:<service> or file:<path>")
	inject := flag.String("inject", "api.anthropic.com", "comma-separated hosts to inject the key on")
	flag.Parse()

	if *placeholder == "" {
		log.Fatal("x01: -placeholder is required")
	}
	realKey, err := readKey(*keySource)
	if err != nil {
		log.Fatalf("x01: read key: %v", err)
	}
	p, err := newProxy(*placeholder, realKey, strings.Split(*inject, ","), nil)
	if err != nil {
		log.Fatal(err)
	}
	pemCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.caCert.Raw})
	if err := os.WriteFile(*caOut, pemCA, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("x01: listening on %s, CA in %s, key from %s (%d bytes, placeholder %d), inject on %s",
		*listen, *caOut, *keySource, len(realKey), len(*placeholder), *inject)
	log.Fatal(http.ListenAndServe(*listen, p))
}

// readKey reads the real key from the caller's Keychain with security(1),
// or from a file for the self-test.
func readKey(src string) (string, error) {
	kind, arg, ok := strings.Cut(src, ":")
	if !ok {
		return "", fmt.Errorf("bad key source %q", src)
	}
	switch kind {
	case "keychain":
		out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", arg, "-w").Output()
		if err != nil {
			return "", fmt.Errorf("security find-generic-password -s %s: %w", arg, err)
		}
		return strings.TrimSpace(string(out)), nil
	case "file":
		b, err := os.ReadFile(arg)
		return strings.TrimSpace(string(b)), err
	}
	return "", fmt.Errorf("bad key source %q", src)
}

func newProxy(placeholder, realKey string, inject []string, upstream *tls.Config) (*proxy, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "x01-model-credential throwaway CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	hosts := map[string]bool{}
	for _, h := range inject {
		if h = strings.TrimSpace(h); h != "" {
			hosts[h] = true
		}
	}
	if upstream == nil {
		upstream = &tls.Config{}
	}
	return &proxy{
		caCert: cert, caKey: key, placeholder: placeholder, realKey: realKey,
		inject: hosts, upstream: upstream, leaves: map[string]*tls.Certificate{},
	}, nil
}

func (p *proxy) leaf(host string) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.leaves[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, p.caCert.Raw}, PrivateKey: key}
	p.leaves[host] = c
	return c, nil
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		log.Printf("x01: plain %s %s (refused)", r.Method, r.URL)
		http.Error(w, "x01: only CONNECT", http.StatusForbidden)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("x01: CONNECT %s:%s", host, port)
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	c, err := p.leaf(host)
	if err != nil {
		log.Printf("x01: leaf %s: %v", host, err)
		return
	}
	tconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*c}, NextProtos: []string{"http/1.1"}})
	if err := tconn.Handshake(); err != nil {
		log.Printf("x01: client handshake %s: %v", host, err)
		return
	}
	p.relay(tconn, r.Host, host)
}

// relay reads HTTP/1.1 requests from the client, rewrites the key on
// inject hosts, and forwards each to the real upstream.
func (p *proxy) relay(client *tls.Conn, hostport, host string) {
	// DisableCompression: pass the client's Accept-Encoding and the
	// upstream's encoded body through untouched, so lengths stay true.
	tr := &http.Transport{TLSClientConfig: p.upstream.Clone(), ForceAttemptHTTP2: true, DisableCompression: true}
	defer tr.CloseIdleConnections()
	br := bufio.NewReader(client)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("x01: read request %s: %v", host, err)
			}
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = hostport
		req.RequestURI = ""
		verdict := p.rewrite(req, host)
		log.Printf("x01: %s https://%s%s headers=[%s] anthropic-beta=%q key=%s",
			req.Method, host, req.URL.Path, headerNames(req.Header), req.Header.Get("Anthropic-Beta"), verdict)
		if tokenPath(req.URL.Path) {
			log.Printf("x01: TOKEN-PATH %s https://%s%s (OAuth or token refresh; body not logged)", req.Method, host, req.URL.Path)
		}
		resp, err := tr.RoundTrip(req)
		if err != nil {
			log.Printf("x01: upstream %s: %v", host, err)
			resp = &http.Response{
				StatusCode: http.StatusBadGateway, ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{}, Body: io.NopCloser(strings.NewReader("x01: upstream error\n")),
			}
		}
		log.Printf("x01: %s https://%s%s -> %s %d", req.Method, host, req.URL.Path, resp.Proto, resp.StatusCode)
		toHTTP11(resp)
		err = resp.Write(client)
		resp.Body.Close()
		if err != nil || resp.Close || req.Close {
			return
		}
	}
}

// toHTTP11 makes an upstream response, often HTTP/2, writable to the
// HTTP/1.1 client. Response.Write takes the status line from ProtoMajor
// and ProtoMinor, so an h2 response would go out as "HTTP/2.0 200".
// An h2 body of unknown length (SSE from /v1/messages) has
// ContentLength -1 and no Transfer-Encoding, which Response.Write sends
// unframed with a connection close; chunked keeps the connection and
// streams each read as a chunk. Trailers ride on the chunked encoding.
func toHTTP11(resp *http.Response) {
	resp.Proto, resp.ProtoMajor, resp.ProtoMinor = "HTTP/1.1", 1, 1
	if resp.ContentLength < 0 && resp.Request != nil && resp.Request.Method != http.MethodHead &&
		resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		resp.TransferEncoding = []string{"chunked"}
	}
	if len(resp.Trailer) > 0 && resp.ContentLength >= 0 {
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		resp.TransferEncoding = []string{"chunked"}
	}
}

// rewrite replaces the placeholder in x-api-key and Authorization on
// inject hosts, as a bare value or inside "Bearer <placeholder>". It
// reports each header and scheme it saw, never a value.
func (p *proxy) rewrite(req *http.Request, host string) string {
	var found []string
	for _, h := range []string{"X-Api-Key", "Authorization"} {
		v := req.Header.Get(h)
		if v == "" {
			continue
		}
		what := h + scheme(v)
		switch {
		case !strings.Contains(v, p.placeholder):
			found = append(found, what+":other-credential")
		case !p.inject[host]:
			// Placeholder sent to a host that gets no key: leave it, flag it.
			found = append(found, what+":placeholder-not-injected")
		default:
			req.Header.Set(h, strings.ReplaceAll(v, p.placeholder, p.realKey))
			found = append(found, what+":injected")
		}
	}
	if len(found) == 0 {
		return "none"
	}
	return strings.Join(found, " ")
}

// scheme names the auth scheme of a header value ("Bearer <x>" gives
// ":Bearer"), or "" for a bare value. Only the first word is kept.
func scheme(v string) string {
	if word, _, ok := strings.Cut(v, " "); ok && word != "" && len(word) < 20 {
		return ":" + word
	}
	return ""
}

// tokenPath reports whether a request looks like an OAuth flow or a
// token refresh, so the log marks it.
func tokenPath(path string) bool {
	l := strings.ToLower(path)
	return strings.Contains(l, "oauth") || strings.Contains(l, "token")
}

func headerNames(h http.Header) string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, strings.ToLower(k))
	}
	return strings.Join(names, ",")
}
