package policyfile

import (
	"strings"
	"testing"
)

// TestLoadModelTokenPath feeds each form of a rule that can allow POST
// to an OAuth token path on a model API host (C03, "Policy loader") and
// checks that each is refused with model-token-path, and that the
// message says how the credential is set.
func TestLoadModelTokenPath(t *testing.T) {
	rest := func(host, method, path string) string {
		return withRule("{host: '" + host + "', port: 443, protocol: rest, rules: [{allow: {method: '" + method + "', path: '" + path + "'}}]}")
	}
	tests := []struct {
		name string
		text string
	}{
		{"exact", rest("api.anthropic.com", "POST", "/v1/oauth/token")},
		{"prefix glob", rest("api.anthropic.com", "POST", "/v1/oauth/token*")},
		{"subtree", rest("api.anthropic.com", "POST", "/v1/oauth/**")},
		{"everything", rest("api.anthropic.com", "POST", "/**")},
		{"longer path", rest("api.anthropic.com", "POST", "/v1/oauth/token/refresh")},
		{"wildcard method", rest("api.anthropic.com", "*", "/v1/oauth/token")},
		{"lowercase post", rest("api.anthropic.com", "post", "/v1/oauth/token")},
		{"trailing slash", rest("api.anthropic.com", "POST", "/v1/oauth/token/")},
		{"percent-encoded", rest("api.anthropic.com", "POST", "/v1/oauth/%74oken")},
		{"case", rest("api.anthropic.com", "POST", "/V1/OAuth/Token")},
		{"wildcard host", rest("*.anthropic.com", "POST", "/v1/oauth/token")},
		{"access preset", withRule("{host: api.anthropic.com, port: 443, protocol: rest, access: read-write}")},
		{"full access", withRule("{host: api.anthropic.com, port: 443, protocol: rest, access: full}")},
		{"L4", withRule("{host: api.anthropic.com, port: 443}")},
		{"other port", withRule("{host: api.anthropic.com, port: 8443}")},
		{"endpoint path", withRule("{host: api.anthropic.com, port: 443, path: /v1/**}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, rs := loadString(t, global, tt.text)
			r := wantRefused(t, rs, RuleModelTokenPath, "")
			for _, want := range []string{"claude setup-token", "wb cred set", testFile, `"r"`} {
				if !strings.Contains(r.Error(), want) {
					t.Errorf("refusal %q doesn't say %q", r.Error(), want)
				}
			}
		})
	}
}

// TestLoadGraphQLPath feeds rules on api.github.com that can match its
// GraphQL path (C03, "Profile parts"), with any method.
func TestLoadGraphQLPath(t *testing.T) {
	for _, path := range []string{"/graphql", "/graphql/", "/graph*", "/%67raphql", "/**", "/GraphQL"} {
		for _, method := range []string{"POST", "GET", "*"} {
			t.Run(method+" "+path, func(t *testing.T) {
				text := withRule("{host: api.github.com, port: 443, protocol: rest, rules: [{allow: {method: '" + method + "', path: '" + path + "'}}]}")
				_, rs := loadString(t, global, text)
				wantRefused(t, rs, RuleGraphQLPath, "api.github.com")
			})
		}
	}
	t.Run("gitlab", func(t *testing.T) {
		_, rs := loadString(t, global, withRule("{host: gitlab.com, port: 443, protocol: rest, rules: [{allow: {method: POST, path: /api/graphql}}]}"))
		wantRefused(t, rs, RuleGraphQLPath, "gitlab.com")
	})
	t.Run("L4", func(t *testing.T) {
		_, rs := loadString(t, global, withRule("{host: api.github.com, port: 443}"))
		wantRefused(t, rs, RuleGraphQLPath, "")
	})
}
