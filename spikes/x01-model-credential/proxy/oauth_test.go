package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	fakeOAuthPlaceholder = "sk-ant-oat01-x01-placeholder-AAAA"
	fakeOAuthReal        = "sk-ant-oat01-FAKE-REAL-TOKEN-FOR-SELF-TEST"
)

// TestBearerInjectedUpstream runs "Authorization: Bearer <placeholder>"
// through the proxy to an HTTP/2 fake API, which must see the real token.
func TestBearerInjectedUpstream(t *testing.T) {
	var seen string
	api, c := h2Setup(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		io.WriteString(w, "ok")
	})
	// h2Setup's proxy uses placeholder "ph" and key "real".
	req, _ := http.NewRequest("POST", api.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer ph")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen != "Bearer real" {
		t.Fatalf("upstream saw Authorization %q, want %q", seen, "Bearer real")
	}
}

func TestRewriteVerdicts(t *testing.T) {
	p, err := newProxy(fakeOAuthPlaceholder, fakeOAuthReal, []string{"api.anthropic.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, host, header, value, wantVerdict, wantValue string
	}{
		{"bearer injected", "api.anthropic.com", "Authorization", "Bearer " + fakeOAuthPlaceholder,
			"Authorization:Bearer:injected", "Bearer " + fakeOAuthReal},
		{"bare x-api-key injected", "api.anthropic.com", "X-Api-Key", fakeOAuthPlaceholder,
			"X-Api-Key:injected", fakeOAuthReal},
		{"bearer on other host", "claude.ai", "Authorization", "Bearer " + fakeOAuthPlaceholder,
			"Authorization:Bearer:placeholder-not-injected", "Bearer " + fakeOAuthPlaceholder},
		{"other credential", "api.anthropic.com", "Authorization", "Bearer something-else",
			"Authorization:Bearer:other-credential", "Bearer something-else"},
		{"no credential", "api.anthropic.com", "", "", "none", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "https://"+tc.host+"/v1/messages", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			v := p.rewrite(req, tc.host)
			if v != tc.wantVerdict {
				t.Fatalf("verdict %q, want %q", v, tc.wantVerdict)
			}
			if strings.Contains(v, fakeOAuthReal) || strings.Contains(v, fakeOAuthPlaceholder) {
				t.Fatalf("verdict %q holds a credential", v)
			}
			if tc.header != "" && req.Header.Get(tc.header) != tc.wantValue {
				t.Fatalf("header %q, want %q", req.Header.Get(tc.header), tc.wantValue)
			}
		})
	}
}

func TestTokenPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/oauth/token":     true,
		"/api/oauth/profile":  true,
		"/v1/messages":        false,
		"/api/event_logging/": false,
	} {
		if tokenPath(path) != want {
			t.Errorf("tokenPath(%q) = %v, want %v", path, !want, want)
		}
	}
}
