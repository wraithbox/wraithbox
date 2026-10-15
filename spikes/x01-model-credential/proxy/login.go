package main

// The browser login part of X01-model-credential (issue #15): deny
// rules, field-name logging on OAuth paths, a full-request placeholder
// scan, and the refresh exchange that keeps the real refresh token in
// the proxy.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxBody caps how much of a request or token response the proxy buffers.
const maxBody = 32 << 20

// publicFields are OAuth body fields whose values aren't secrets, so the
// log keeps them. Every other field is logged by name only.
var publicFields = map[string]bool{
	"grant_type": true, "client_id": true, "redirect_uri": true, "scope": true,
	"token_type": true, "expires_in": true, "code_challenge_method": true,
	"error": true, "error_description": true, "response_type": true,
}

// denyRule refuses a host, or a path prefix on a host.
type denyRule struct{ host, prefix string }

func parseDeny(s string) []denyRule {
	var rules []denyRule
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		host, path, _ := strings.Cut(r, "/")
		rules = append(rules, denyRule{host: host, prefix: "/" + path})
	}
	return rules
}

func (p *proxy) denied(host, path string) bool {
	for _, r := range p.deny {
		if r.host == host && strings.HasPrefix(path, r.prefix) {
			return true
		}
	}
	return false
}

// oauthState holds the real login. Claude Code sees only the two
// placeholders.
type oauthState struct {
	mu        sync.Mutex
	accessPH  string
	refreshPH string
	access    string
	refresh   string
	store     string // keychain service, "" for the self-test
	refreshes int
}

type storedLogin struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
}

func loadOAuth(store, accessPH, refreshPH string) (*oauthState, error) {
	kind, svc, ok := strings.Cut(store, ":")
	if !ok || kind != "keychain" {
		return nil, fmt.Errorf("bad -oauth-store %q, want keychain:<service>", store)
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", svc, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("security find-generic-password -s %s: %w", svc, err)
	}
	var l storedLogin
	if err := json.Unmarshal(bytes.TrimSpace(out), &l); err != nil {
		return nil, fmt.Errorf("item %s is not {access,refresh} JSON", svc)
	}
	if l.Access == "" || l.Refresh == "" {
		return nil, fmt.Errorf("item %s lacks access or refresh", svc)
	}
	return &oauthState{accessPH: accessPH, refreshPH: refreshPH, access: l.Access, refresh: l.Refresh, store: svc}, nil
}

// save writes the current login back to the Keychain item. The value
// goes on security's argv: a spike shortcut on a test account.
func (o *oauthState) save() error {
	if o.store == "" {
		return nil
	}
	b, _ := json.Marshal(storedLogin{Access: o.access, Refresh: o.refresh})
	return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", o.store,
		"-a", "x01", "-T", "/usr/bin/security", "-w", string(b)).Run()
}

func (o *oauthState) currentAccess() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.access
}

// readBody buffers a request body so it can be scanned and rewritten.
func readBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("body over %d bytes", maxBody)
	}
	return b, nil
}

func setBody(req *http.Request, b []byte) {
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.ContentLength = int64(len(b))
	req.Header.Set("Content-Length", strconv.Itoa(len(b)))
}

// placeholderSpots lists where in a request a placeholder appears,
// besides an Authorization or x-api-key header the proxy injects.
// It runs before the rewrite.
func (p *proxy) placeholderSpots(req *http.Request, host string, body []byte) []string {
	phs := p.placeholders()
	has := func(s string) bool {
		for _, ph := range phs {
			if strings.Contains(s, ph) {
				return true
			}
		}
		return false
	}
	var spots []string
	for name, vals := range req.Header {
		injected := p.inject[host] && (name == "Authorization" || name == "X-Api-Key")
		for _, v := range vals {
			if has(v) && !injected {
				spots = append(spots, "header:"+strings.ToLower(name))
			}
		}
	}
	if has(req.URL.RawQuery) {
		spots = append(spots, "query")
	}
	if len(body) > 0 && has(string(body)) {
		spots = append(spots, "body")
	}
	sort.Strings(spots)
	return spots
}

func (p *proxy) placeholders() []string {
	var phs []string
	if p.placeholder != "" {
		phs = append(phs, p.placeholder)
	}
	if p.oauth != nil && p.oauth.refreshPH != "" {
		phs = append(phs, p.oauth.refreshPH)
	}
	return phs
}

// fields describes a JSON or form body: each field name, with the
// value for publicFields and "<len N>" for the rest.
func fields(body []byte, contentType string) string {
	if len(body) == 0 {
		return "(empty)"
	}
	kv := map[string]string{}
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		for k, v := range obj {
			kv[k] = describe(k, v)
		}
	} else if vals, err := url.ParseQuery(string(body)); err == nil && strings.Contains(contentType, "form") {
		for k, v := range vals {
			kv[k] = describe(k, strings.Join(v, ","))
		}
	} else {
		return fmt.Sprintf("(not JSON or form, %d bytes, content-type %q)", len(body), contentType)
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+kv[k])
	}
	return strings.Join(parts, " ")
}

func describe(k string, v any) string {
	switch t := v.(type) {
	case string:
		if publicFields[k] {
			return strconv.Quote(t)
		}
		return fmt.Sprintf("<len %d>", len(t))
	case float64, bool, nil:
		return fmt.Sprint(t)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for kk := range t {
			keys = append(keys, kk)
		}
		sort.Strings(keys)
		return "{" + strings.Join(keys, ",") + "}"
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

// swapRefresh puts the real refresh token into a refresh request that
// carries the refresh placeholder. It reports whether it did.
func (p *proxy) swapRefresh(req *http.Request, host string, body []byte) ([]byte, bool) {
	o := p.oauth
	if o == nil || !bytes.Contains(body, []byte(o.refreshPH)) {
		return body, false
	}
	if !p.inject[host] {
		log.Printf("x01: REFRESH-PLACEHOLDER-NOT-INJECTED %s https://%s%s", req.Method, host, req.URL.Path)
		return body, false
	}
	o.mu.Lock()
	real := o.refresh
	o.mu.Unlock()
	return bytes.ReplaceAll(body, []byte(o.refreshPH), []byte(real)), true
}

// tokenResponse logs a token response by field name, and on a refresh
// stores the new real tokens and hands the client placeholders. It
// returns the body to send.
func (p *proxy) tokenResponse(resp *http.Response, label string, refreshed bool) []byte {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if err != nil {
		log.Printf("x01: %s read response: %v", label, err)
	}
	log.Printf("x01: TOKEN-RESPONSE %s status=%d content-encoding=%q fields=[%s]",
		label, resp.StatusCode, resp.Header.Get("Content-Encoding"), fields(body, resp.Header.Get("Content-Type")))
	if p.oauth != nil {
		body = p.oauth.takeTokens(body, label, refreshed && resp.StatusCode == http.StatusOK)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.TransferEncoding = nil
	return body
}

// takeTokens keeps the real tokens of a refresh response and replaces
// them with the placeholders. Outside a refresh it only checks that no
// real token the proxy holds goes to the client.
func (o *oauthState) takeTokens(body []byte, label string, refreshed bool) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	var tr struct {
		Access    string  `json:"access_token"`
		Refresh   string  `json:"refresh_token"`
		ExpiresIn float64 `json:"expires_in"`
	}
	if refreshed && json.Unmarshal(body, &tr) == nil && tr.Access != "" {
		rotated := tr.Refresh != "" && tr.Refresh != o.refresh
		o.refreshes++
		log.Printf("x01: REFRESH #%d %s access-changed=%v refresh-returned=%v refresh-rotated=%v expires_in=%v",
			o.refreshes, label, tr.Access != o.access, tr.Refresh != "", rotated, tr.ExpiresIn)
		o.access = tr.Access
		if tr.Refresh != "" {
			o.refresh = tr.Refresh
		}
		if err := o.save(); err != nil {
			log.Printf("x01: REFRESH-SAVE-FAILED %s: %v (the new tokens are in memory only)", o.store, err)
		}
		body = bytes.ReplaceAll(body, []byte(tr.Access), []byte(o.accessPH))
		if tr.Refresh != "" {
			body = bytes.ReplaceAll(body, []byte(tr.Refresh), []byte(o.refreshPH))
		}
	}
	for _, real := range []string{o.access, o.refresh} {
		if real != "" && bytes.Contains(body, []byte(real)) {
			log.Printf("x01: REAL-TOKEN-TO-CLIENT %s (replaced)", label)
			body = bytes.ReplaceAll(body, []byte(real), []byte("x01-withheld"))
		}
	}
	return body
}
