package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"
)

// h2Setup starts an HTTP/2 TLS upstream with handler h, the proxy in
// front of it, and returns an HTTP/1.1 client that goes through the proxy.
func h2Setup(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("upstream got %s, want HTTP/2", r.Proto)
		}
		h(w, r)
	}))
	api.EnableHTTP2 = true
	api.StartTLS()
	t.Cleanup(api.Close)

	roots := x509.NewCertPool()
	roots.AddCert(api.Certificate())
	p, err := newProxy("ph", "real", []string{"127.0.0.1"}, &tls.Config{RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(p)
	t.Cleanup(ps.Close)
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(p.caCert)
	pu, _ := url.Parse(ps.URL)
	// The proxy offers only http/1.1 to the client, as to Claude Code.
	return api, &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{RootCAs: clientRoots},
	}}
}

// TestH2UpstreamFixedLength is the Claude Code failure: an h2 401 with a
// body of known length must reach the HTTP/1.1 client as a parsable
// HTTP/1.1 response, and the connection must carry a second request.
func TestH2UpstreamFixedLength(t *testing.T) {
	api, c := h2Setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "real" {
			t.Errorf("upstream saw key %q", r.Header.Get("X-Api-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "19")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid"}`)
	})
	reused := 0
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", api.URL+"/v1/messages", strings.NewReader("{}"))
		req.Header.Set("X-Api-Key", "ph")
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(i httptrace.GotConnInfo) {
				if i.Reused {
					reused++
				}
			},
		}))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.ProtoMajor != 1 || resp.StatusCode != 401 || string(body) != `{"error":"invalid"}` {
			t.Fatalf("round %d: %s %d %q", i, resp.Proto, resp.StatusCode, body)
		}
	}
	if reused != 1 {
		t.Fatalf("connection reused %d times, want 1", reused)
	}
}

// TestH2UpstreamStreaming checks SSE of unknown length streams through:
// the upstream sends the next event only after the client has read the
// previous one, so a proxy that buffers the body would time out.
func TestH2UpstreamStreaming(t *testing.T) {
	got := make(chan struct{})
	const events = 3
	api, c := h2Setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < events; i++ {
			fmt.Fprintf(w, "data: event %d\n\n", i)
			w.(http.Flusher).Flush()
			select {
			case <-got:
			case <-time.After(5 * time.Second):
				t.Errorf("client never read event %d: the proxy buffers", i)
				return
			}
		}
	})
	req, _ := http.NewRequest("POST", api.URL+"/v1/messages", strings.NewReader("{}"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 1 || len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
		t.Fatalf("got %s, Transfer-Encoding %v; want HTTP/1.1 chunked", resp.Proto, resp.TransferEncoding)
	}
	br := bufio.NewReader(resp.Body)
	for i := 0; i < events; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if want := fmt.Sprintf("data: event %d\n", i); line != want {
			t.Fatalf("event %d: %q, want %q", i, line, want)
		}
		br.ReadString('\n') // blank line ending the event
		got <- struct{}{}
	}
	if rest, _ := io.ReadAll(br); len(rest) != 0 {
		t.Fatalf("trailing bytes %q", rest)
	}
}
