package policyfile

import (
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// TestLoadUpstreamExample loads OpenShell v0.1.2's
// examples/multi-agent-notepad policy, reduced to the keys Wraith Box
// implements (see the file's header), as each source that may hold it.
func TestLoadUpstreamExample(t *testing.T) {
	data := readTestdata(t, "openshell-multi-agent-notepad.yaml")
	for _, src := range []policy.Source{global, project, repo} {
		t.Run(src.Kind.String(), func(t *testing.T) {
			f := mustLoad(t, src, data)
			if len(f.Endpoints) != 6 {
				t.Fatalf("got %d endpoints, want 6", len(f.Endpoints))
			}
			wantRuleNames(t, f, src, map[string]int{"codex": 4, "codex-plugins": 1, "github-memory": 1})
			gh := f.Endpoints[5]
			if gh.Host.String() != "api.github.com" || len(gh.Rules) != 4 || gh.Rules[3].Method != "PUT" {
				t.Errorf("github-memory endpoint = %+v", gh)
			}
		})
	}
}

// wantRuleNames checks the number of endpoints of each rule name, and
// that every endpoint has the file's source.
func wantRuleNames(t *testing.T, f *File, src policy.Source, want map[string]int) {
	t.Helper()
	names := map[string]int{}
	for _, e := range f.Endpoints {
		names[e.RuleName]++
		if e.Source != src {
			t.Errorf("endpoint %s has source %v, want %v", e.Host, e.Source, src)
		}
	}
	for n, c := range want {
		if names[n] != c {
			t.Errorf("rule %s has %d endpoints, want %d (all: %v)", n, names[n], c, names)
		}
	}
}

func TestLoadAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  policy.Source
		text string
	}{
		{"empty file", repo, ""},
		{"comment only", repo, "# nothing\n"},
		{"version only", repo, "version: 1\n"},
		{"empty rules", repo, "version: 1\nnetwork_policies: {}\n"},
		{"no protocol is L4", repo, withRule("{host: example.com, port: 443}")},
		{"ports list", repo, withRule("{host: example.com, ports: [443, 8443]}")},
		{"wildcard host", repo, withRule("{host: '*.example.com', port: 443}")},
		{"endpoint path", repo, withRule("{host: example.com, port: 443, path: /api/**, protocol: rest, access: read-only}")},
		{"deny rules", repo, withRule("{host: example.com, port: 443, protocol: rest, access: full, deny_rules: [{method: DELETE, path: /**}]}")},
		{"audit from project", project, withRule("{host: example.com, port: 443, enforcement: audit}")},
		{"core tags", repo, "version: !!int 1\nnetwork_policies: !!map {}\n"},
		{"rule name differs from key", repo, strings.Replace(withRule("{host: example.com, port: 443}"), "  r:\n", "  r:\n    name: other\n", 1)},
		{"extension from global", global, withRuleAndExt(
			"  pass:\n    - {host: pass.example.com, reason: pinned certificate}\n"+
				"  raw_tcp:\n    - {host: db.example.com, port: 5432, reason: postgres}\n"+
				"  wildcard_budget: 100\n"+
				"  parts_off:\n    - {profile: git-hosting, kind: github, part: graphql}\n",
			"{host: example.com, port: 443}")},
		{"token path GET", global, withRule("{host: api.anthropic.com, port: 443, protocol: rest, rules: [{allow: {method: GET, path: /v1/oauth/token}}]}")},
		{"other oauth path", global, withRule("{host: api.anthropic.com, port: 443, protocol: rest, rules: [{allow: {method: POST, path: /v1/oauth/hello}}]}")},
		{"token path on claude.ai", global, withRule("{host: claude.ai, port: 443, protocol: rest, rules: [{allow: {method: POST, path: /v1/oauth/token}}]}")},
		{"graphql path elsewhere", global, withRule("{host: example.com, port: 443, protocol: rest, rules: [{allow: {method: POST, path: /graphql}}]}")},
		{"other path on api.github.com", global, withRule("{host: api.github.com, port: 443, protocol: rest, rules: [{allow: {method: GET, path: /repos/**}}]}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustLoad(t, tt.src, tt.text)
		})
	}
}

func TestLoadExtension(t *testing.T) {
	text := withRuleAndExt(
		"  pass:\n    - {host: pass.example.com, reason: pinned certificate}\n"+
			"  raw_tcp:\n    - {host: db.example.com, port: 5432, reason: postgres}\n"+
			"  wildcard_budget: 100\n"+
			"  parts_off:\n    - {profile: git-hosting, kind: github, part: graphql}\n",
		"{host: example.com, port: 443}")
	f := mustLoad(t, project, text)
	if len(f.PassHosts) != 1 || f.PassHosts[0].Host.String() != "pass.example.com" || f.PassHosts[0].Source != project {
		t.Errorf("pass hosts = %+v", f.PassHosts)
	}
	if len(f.RawTCP) != 1 || f.RawTCP[0].Port != 5432 || f.RawTCP[0].Reason != "postgres" {
		t.Errorf("raw TCP = %+v", f.RawTCP)
	}
	if f.WildcardBudget == nil || *f.WildcardBudget != 100 {
		t.Errorf("wildcard budget = %v", f.WildcardBudget)
	}
	if len(f.PartsOff) != 1 || f.PartsOff[0].Part != "graphql" {
		t.Errorf("parts off = %+v", f.PartsOff)
	}
}

// TestLoadRefuses feeds each load-time refusal a file that breaks it,
// and checks the rule, that the message names the file, and that it
// shows the value at fault.
func TestLoadRefuses(t *testing.T) {
	ep := "{host: example.com, port: 443}"
	tests := []struct {
		name  string
		src   policy.Source
		text  string
		rule  string
		value string
	}{
		// Decision 3: the YAML form.
		{"anchor", global, "version: &v 1\n", RuleYAMLForm, "v"},
		{"alias", global, "a: &v 1\nb: *v\n", RuleYAMLForm, "v"},
		{"merge key", global, "version: 1\nnetwork_policies:\n  r:\n    <<: {name: x}\n", RuleYAMLForm, "<<"},
		{"custom tag", global, "version: !thing 1\n", RuleYAMLForm, "!thing"},
		{"binary tag", global, "version: !!binary aGk=\n", RuleYAMLForm, "!!binary"},
		{"duplicate key", global, "version: 1\nversion: 1\n", RuleYAMLForm, "version"},
		{"non-string key", global, "version: 1\n1: x\n", RuleYAMLForm, "1"},
		{"second document", global, "version: 1\n---\nversion: 1\n", RuleYAMLForm, ""},
		{"syntax", global, "version: [1\n", RuleYAMLSyntax, ""},
		// Decision 5: closed keys and the version.
		{"no version", global, "network_policies: {}\n", RuleVersion, ""},
		{"version 2", global, "version: 2\n", RuleVersion, "2"},
		{"version string", global, "version: '1'\n", RuleVersion, "1"},
		{"unknown top key", global, "version: 1\nnetwork_policy: {}\n", RuleKeyUnknown, "network_policy"},
		{"refused top key", global, "version: 1\nfilesystem_policy: {}\n", RuleKeyRefused, "filesystem_policy"},
		{"unknown rule key", global, strings.Replace(withRule(ep), "    endpoints:", "    hosts: []\n    endpoints:", 1), RuleKeyUnknown, "hosts"},
		{"unknown binary key", global, strings.Replace(withRule(ep), `path: "/**"`, "path: \"/**\"\n        sha: x", 1), RuleKeyUnknown, "sha"},
		{"refused endpoint key", global, withRule("{host: example.com, port: 443, tls: terminate}"), RuleKeyRefused, "tls"},
		{"unknown endpoint key", global, withRule("{host: example.com, port: 443, hostname: x}"), RuleKeyUnknown, "hostname"},
		{"unknown rules key", global, withRule("{host: example.com, port: 443, protocol: rest, rules: [{deny: {method: GET, path: /}}]}"), RuleKeyUnknown, "deny"},
		{"refused allow key", global, withRule("{host: example.com, port: 443, protocol: rest, rules: [{allow: {method: GET, path: /, query: {a: b}}}]}"), RuleKeyRefused, "query"},
		{"refused deny key", global, withRule("{host: example.com, port: 443, protocol: rest, access: full, deny_rules: [{method: GET, path: /, tool: x}]}"), RuleKeyRefused, "tool"},
		// Decision 6: the extension key.
		{"unsupported extension", global, "version: 1\nwraithbox:\n  credential_bindings: []\n", RuleKeyUnsupported, "credential_bindings"},
		{"unknown extension", global, "version: 1\nwraithbox:\n  passes: []\n", RuleKeyUnknown, "passes"},
		{"unknown pass key", global, "version: 1\nwraithbox:\n  pass:\n    - {host: a.example.com, reason: r, port: 1}\n", RuleKeyUnknown, "port"},
		// Decision 7: sources.
		{"extension from repository", repo, "version: 1\nwraithbox:\n  wildcard_budget: 1\n", RuleSource, ""},
		{"audit from repository", repo, withRule("{host: example.com, port: 443, enforcement: audit}"), RuleAuditSource, "example.com"},
		// Decision 8: values.
		{"empty host", global, withRule("{host: '', port: 443}"), "", ""},
		{"long rule name", global, strings.Replace(withRule(ep), "  r:\n", "  "+strings.Repeat("n", 129)+":\n", 1), RuleString, strings.Repeat("n", maxValueShown)},
		{"control byte in name", global, strings.Replace(withRule(ep), "  r:\n", "  r:\n    name: \"a\\tb\"\n", 1), RuleString, "a\tb"},
		{"empty name", global, strings.Replace(withRule(ep), "  r:\n", "  r:\n    name: ''\n", 1), RuleString, ""},
		{"underscore host", global, withRule("{host: my_host.example.com, port: 443}"), "dns-name-form", "my_host.example.com"},
		{"non-ASCII host", global, withRule("{host: \u00e9t\u00e9.example, port: 443}"), "dns-name-form", "\u00e9t\u00e9.example"},
		{"port 0", global, withRule("{host: example.com, port: 0}"), RulePort, "0"},
		{"port 65536", global, withRule("{host: example.com, port: 65536}"), RulePort, "65536"},
		{"port string", global, withRule("{host: example.com, port: '443'}"), RulePort, "443"},
		{"port hex", global, withRule("{host: example.com, port: 0x1bb}"), RulePort, "0x1bb"},
		{"port and ports", global, withRule("{host: example.com, port: 443, ports: [443]}"), RulePort, ""},
		{"no port", global, withRule("{host: example.com}"), RulePort, ""},
		{"bad port in list", global, withRule("{host: example.com, ports: [443, -1]}"), RulePort, "-1"},
		{"no binaries", global, strings.Replace(withRule(ep), "    binaries:\n      - path: \"/**\"\n", "", 1), RuleBinaryNarrowing, ""},
		{"narrowed binary", global, strings.Replace(withRule(ep), `"/**"`, "/usr/bin/curl", 1), RuleBinaryNarrowing, "/usr/bin/curl"},
		{"empty binaries", global, strings.Replace(withRule(ep), "    binaries:\n      - path: \"/**\"\n", "    binaries: []\n", 1), RuleBinaryNarrowing, ""},
		{"CONNECT allowed", global, withRule("{host: example.com, port: 443, protocol: rest, rules: [{allow: {method: CONNECT, path: /**}}]}"), RuleTunnel, "CONNECT"},
		{"CONNECT denied", global, withRule("{host: example.com, port: 443, protocol: rest, access: full, deny_rules: [{method: connect, path: /**}]}"), RuleTunnel, "connect"},
		{"graphql protocol", global, withRule("{host: example.com, port: 443, protocol: graphql}"), RuleProtocolUnsupported, "graphql"},
		{"websocket protocol", global, withRule("{host: example.com, port: 443, protocol: websocket}"), RuleProtocolUnsupported, "websocket"},
		{"tcp protocol", global, withRule("{host: example.com, port: 443, protocol: tcp}"), RuleProtocolUnsupported, "tcp"},
		{"unknown access", global, withRule("{host: example.com, port: 443, protocol: rest, access: admin}"), RuleValue, "admin"},
		{"unknown enforcement", global, withRule("{host: example.com, port: 443, enforcement: log}"), RuleValue, "log"},
		{"endpoints not a list", global, "version: 1\nnetwork_policies:\n  r:\n    endpoints: {host: x}\n    binaries: [{path: \"/**\"}]\n", RuleValue, ""},
		{"duplicate rule name", global, withRule(ep) + "  s:\n    name: r\n    endpoints: [" + ep + "]\n    binaries: [{path: \"/**\"}]\n", RuleRuleName, "r"},
		{"builtin rule key", global, strings.Replace(withRule(ep), "  r:\n", "  git-hosting:\n", 1), RuleRuleName, "git-hosting"},
		{"builtin rule name", global, strings.Replace(withRule(ep), "  r:\n", "  r:\n    name: Builtin-github\n", 1), RuleRuleName, "Builtin-github"},
		{"pass wildcard", global, "version: 1\nwraithbox:\n  pass:\n    - {host: '*.example.com', reason: r}\n", RulePassHost, "*.example.com"},
		{"pass without reason", global, "version: 1\nwraithbox:\n  pass:\n    - {host: a.example.com}\n", RulePassHost, ""},
		{"pass covered host", global, "version: 1\nwraithbox:\n  pass:\n    - {host: github.com, reason: r}\n", RulePassHost, "github.com"},
		{"pass underscore host", global, "version: 1\nwraithbox:\n  pass:\n    - {host: a_b.example.com, reason: r}\n", "dns-name-form", "a_b.example.com"},
		{"raw TCP covered host", global, "version: 1\nwraithbox:\n  raw_tcp:\n    - {host: api.anthropic.com, port: 22, reason: r}\n", RuleRawTCP, "api.anthropic.com"},
		{"raw TCP wildcard", global, "version: 1\nwraithbox:\n  raw_tcp:\n    - {host: '*.example.com', port: 22, reason: r}\n", RuleRawTCP, "*.example.com"},
		{"raw TCP without reason", global, "version: 1\nwraithbox:\n  raw_tcp:\n    - {host: db.example.com, port: 22}\n", RuleRawTCP, ""},
		{"raw TCP without port", global, "version: 1\nwraithbox:\n  raw_tcp:\n    - {host: db.example.com, reason: r}\n", RulePort, ""},
		{"audit on covered host", global, withRule("{host: '*.github.com', port: 443, enforcement: audit}"), RuleAuditGuarded, "*.github.com"},
		{"protocol conflict", global, withRuleAndExt("  raw_tcp:\n    - {host: example.com, port: 443, reason: r}\n", ep), RuleProtocolConflict, "example.com:443"},
		{"wildcard budget too big", global, "version: 1\nwraithbox:\n  wildcard_budget: 65536\n", RuleValue, "65536"},
		{"unknown part", global, "version: 1\nwraithbox:\n  parts_off:\n    - {profile: git-hosting, kind: github, part: nope}\n", policy.RulePart, "git-hosting/github/nope"},
		{"part without profile", global, "version: 1\nwraithbox:\n  parts_off:\n    - {part: nope}\n", RuleValue, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, rs := loadString(t, tt.src, tt.text)
			if tt.rule == "" {
				if rs == nil {
					t.Fatal("the file loaded, want a refusal")
				}
				return
			}
			wantRefused(t, rs, tt.rule, tt.value)
		})
	}
}

// TestLoadRefusalNamesLine checks that a refusal names the line and key
// path of the value at fault.
func TestLoadRefusalNamesLine(t *testing.T) {
	_, rs := loadString(t, global, withRule("{host: example.com, port: 443}", "{host: example.com, port: 0}"))
	r := wantRefused(t, rs, RulePort, "0")
	if r.Line != 6 || r.Key != "network_policies.r.endpoints[1].port" {
		t.Errorf("refusal at line %d key %q, want line 6 key network_policies.r.endpoints[1].port", r.Line, r.Key)
	}
}

// TestLoadFirstRefusalPerRule checks decision 10: one refusal per rule,
// the first.
func TestLoadFirstRefusalPerRule(t *testing.T) {
	_, rs := loadString(t, global, withRule(
		"{host: example.com, port: 0}", "{host: example.com, port: 70000}", "{host: a_b.example.com, port: 443}"))
	ports := 0
	for _, r := range rs.List {
		if r.Rule == RulePort {
			ports++
		}
	}
	if ports != 1 {
		t.Errorf("got %d %s refusals, want 1:\n%v", ports, RulePort, rs)
	}
	wantRefused(t, rs, RulePort, "0")
	wantRefused(t, rs, "dns-name-form", "a_b.example.com")
}

// TestLoadRefusalsCapped checks MaxRefusals with a file that breaks
// more rules than that.
func TestLoadRefusalsCapped(t *testing.T) {
	c := newCollector(testFile)
	for i := range MaxRefusals + 5 {
		c.add("rule-"+string(rune('a'+i)), 1, "", "", "x")
	}
	if got := len(c.err().List); got != MaxRefusals {
		t.Errorf("got %d refusals, want %d", got, MaxRefusals)
	}
}

// TestLoadEscapesValues checks that bytes from the file are escaped in
// a refusal, and that a long value is cut.
func TestLoadEscapesValues(t *testing.T) {
	_, rs := loadString(t, global, "version: 1\n\"bad\\e[31m key\": 1\n")
	r := wantRefused(t, rs, RuleKeyUnknown, "bad\x1b[31m key")
	if strings.ContainsRune(r.Error(), 0x1b) {
		t.Errorf("refusal %q holds a raw escape byte", r.Error())
	}
	_, rs = loadString(t, global, "version: 1\n? "+strings.Repeat("k", 5000)+"\n: 1\n")
	r = wantRefused(t, rs, RuleKeyUnknown, "")
	if len(r.Error()) > 400 {
		t.Errorf("refusal of a 5000-byte key has %d bytes", len(r.Error()))
	}
}

func TestLoadSourceWithoutFile(t *testing.T) {
	for _, k := range []policy.SourceKind{policy.SourceUnset, policy.SourceBuiltin, policy.SourceApproval} {
		_, rs := loadString(t, policy.Source{Kind: k}, "version: 1\n")
		wantRefused(t, rs, RuleSource, "")
	}
}
