package main

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestInject runs a client through the proxy to a fake API and checks
// that the fake API sees the real key and the client never does.
func TestInject(t *testing.T) {
	const placeholder = "sk-ant-api03-x01-placeholder"
	const real = "sk-ant-api03-FAKE-REAL-KEY-FOR-SELF-TEST"
	var seen string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Api-Key")
		io.WriteString(w, "ok")
	}))
	defer api.Close()

	upstreamRoots := x509.NewCertPool()
	upstreamRoots.AddCert(api.Certificate())
	p, err := newProxy(placeholder, real, []string{"127.0.0.1"}, &tls.Config{RootCAs: upstreamRoots})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(p)
	defer ps.Close()

	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(p.caCert)
	pu, _ := url.Parse(ps.URL)
	c := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{RootCAs: clientRoots},
	}}
	for i := 0; i < 2; i++ { // twice: keep-alive reuses the inspected connection
		req, _ := http.NewRequest("POST", api.URL+"/v1/messages", strings.NewReader("{}"))
		req.Header.Set("X-Api-Key", placeholder)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "ok" || seen != real {
			t.Fatalf("round %d: body %q, upstream saw key %q", i, body, seen)
		}
		if strings.Contains(string(body), real) {
			t.Fatal("real key reached the client")
		}
	}
}

func TestNoInjectOnOtherHost(t *testing.T) {
	p, err := newProxy("ph", "real", []string{"api.anthropic.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "https://example.com/", nil)
	req.Header.Set("X-Api-Key", "ph")
	if v := p.rewrite(req, "example.com"); v != "X-Api-Key:placeholder-not-injected" {
		t.Fatalf("verdict %q", v)
	}
	if req.Header.Get("X-Api-Key") != "ph" {
		t.Fatal("key injected on a host outside -inject")
	}
}
