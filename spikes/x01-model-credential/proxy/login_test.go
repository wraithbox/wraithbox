package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	accessPH  = "ph"
	refreshPH = "sk-ant-ort01-x01-refresh-placeholder"
)

// TestRefreshSwap runs a refresh through the proxy to a fake token
// endpoint. The endpoint must see the real refresh token, the client
// must get only placeholders back, and the next API call must carry
// the new access token.
func TestRefreshSwap(t *testing.T) {
	var gotRefresh, gotAuth string
	api, c, p := h2SetupProxy(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			gotRefresh = in["refresh_token"]
			io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":28800,"token_type":"Bearer"}`)
		default:
			gotAuth = r.Header.Get("Authorization")
			io.WriteString(w, "ok")
		}
	})
	p.oauth = &oauthState{accessPH: accessPH, refreshPH: refreshPH, access: "real", refresh: "old-refresh"}

	req, _ := http.NewRequest("POST", api.URL+"/v1/oauth/token",
		strings.NewReader(`{"grant_type":"refresh_token","refresh_token":"`+refreshPH+`","client_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if gotRefresh != "old-refresh" {
		t.Fatalf("token endpoint saw refresh %q, want the real one", gotRefresh)
	}
	if bytes.Contains(body, []byte("new-")) {
		t.Fatalf("client got a real token: %s", body)
	}
	if !bytes.Contains(body, []byte(`"access_token":"ph"`)) || !bytes.Contains(body, []byte(refreshPH)) {
		t.Fatalf("client body lacks the placeholders: %s", body)
	}
	if p.oauth.access != "new-access" || p.oauth.refresh != "new-refresh" {
		t.Fatalf("proxy holds %q/%q, want the new tokens", p.oauth.access, p.oauth.refresh)
	}

	req, _ = http.NewRequest("POST", api.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer ph")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer new-access" {
		t.Fatalf("API saw %q, want the refreshed token", gotAuth)
	}
}

func TestDeny(t *testing.T) {
	hit := false
	api, c, p := h2SetupProxy(t, func(w http.ResponseWriter, r *http.Request) { hit = true })
	p.deny = parseDeny("127.0.0.1/v1/oauth/hello")
	resp, err := c.Get(api.URL + "/v1/oauth/hello")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || hit {
		t.Fatalf("status %d, upstream hit %v; want 403 and no hit", resp.StatusCode, hit)
	}
	resp, err = c.Get(api.URL + "/api/hello")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !hit {
		t.Fatalf("other path: status %d, hit %v", resp.StatusCode, hit)
	}
}

func TestPlaceholderSpots(t *testing.T) {
	p, _ := newProxy(accessPH+"-long", "real", []string{"api.anthropic.com"}, nil)
	p.oauth = &oauthState{refreshPH: refreshPH}
	req := httptest.NewRequest("POST", "https://claude.ai/x?t="+refreshPH, nil)
	req.Header.Set("Authorization", "Bearer "+accessPH+"-long")
	req.Header.Set("X-Other", accessPH+"-long")
	got := strings.Join(p.placeholderSpots(req, "claude.ai", []byte(`{"a":"`+refreshPH+`"}`)), " ")
	if got != "body header:authorization header:x-other query" {
		t.Fatalf("spots %q", got)
	}
	req = httptest.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+accessPH+"-long")
	if spots := p.placeholderSpots(req, "api.anthropic.com", nil); len(spots) != 0 {
		t.Fatalf("injected header flagged: %v", spots)
	}
}

func TestFieldsHideSecrets(t *testing.T) {
	got := fields([]byte(`{"grant_type":"authorization_code","code":"SECRET","code_verifier":"SECRET2","expires_in":3600}`), "application/json")
	if strings.Contains(got, "SECRET") {
		t.Fatalf("fields leaked a value: %s", got)
	}
	if !strings.Contains(got, `grant_type="authorization_code"`) || !strings.Contains(got, "code=<len 6>") || !strings.Contains(got, "expires_in=3600") {
		t.Fatalf("fields %s", got)
	}
	got = fields([]byte("grant_type=refresh_token&refresh_token=SECRET"), "application/x-www-form-urlencoded")
	if strings.Contains(got, "SECRET") || !strings.Contains(got, "refresh_token=<len 6>") {
		t.Fatalf("form fields %s", got)
	}
}
