package policyfile

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// TestYAMLv4Refuses covers each input that v4 reads differently from
// v3 in a way a decision could see (I770). Each one is still refused,
// and the comment says what v3 did.
func TestYAMLv4Refuses(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		rule  string
		value string
	}{
		// v3 dropped the non-specific tag and resolved the value as if
		// it had none, so `! 1` was an integer and a string field
		// refused it. v4 makes it the string "1". form refuses the tag.
		{"non-specific tag on an integer", "version: ! 1\n", RuleYAMLForm, "!"},
		{"non-specific tag on a name", strings.Replace(withRule("{host: example.com, port: 443}"), "  r:\n", "  r:\n    name: ! 123\n", 1), RuleYAMLForm, "!"},
		{"non-specific tag on a port", withRule("{host: example.com, port: ! 443}"), RuleYAMLForm, "!"},
		{"verbatim non-specific tag", "version: !<!> 1\n", RuleYAMLForm, "!"},
		// v3 read an empty value with the tag as null, v4 as "".
		{"non-specific tag alone", "version: 1\nnetwork_policies: !\n", RuleYAMLForm, "!"},
		{"non-specific tag on a mapping", "version: 1\nnetwork_policies: ! {}\n", RuleYAMLForm, "!"},
		// v3 tagged -0 as an integer, v4 as a float. Both are refused.
		{"negative zero port", withRule("{host: example.com, port: -0}"), RulePort, "-0"},
		{"negative zero version", "version: -0\n", RuleVersion, "-0"},
		// v3 refused a lone colon as a syntax error. v4 reads a mapping
		// with a null key.
		{"lone colon", ":\n", RuleYAMLForm, ""},
		// The YAML 1.1 forms that v3 and v4 both read as strings, and
		// the integer forms that both read as integers, are refused
		// where a decision would see them.
		{"YAML 1.1 octal port", withRule("{host: example.com, port: 0o17}"), RulePort, "0o17"},
		{"underscore port", withRule("{host: example.com, port: 4_43}"), RulePort, "4_43"},
		{"yes as version", "version: yes\n", RuleVersion, "yes"},
		{"tag directive outside the core schema", "%TAG !e! tag:example.com,2000:\n---\nversion: !e!int 1\n", RuleYAMLForm, ""},
		{"merge key in flow", "version: 1\nnetwork_policies: {<<: {}}\n", RuleYAMLForm, "<<"},
		{"explicit merge tag", "version: 1\n!!merge x: 1\n", RuleYAMLForm, ""},
		{"document end then document", "...\n---\nversion: 1\n---\nversion: 1\n", RuleYAMLForm, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, rs := loadString(t, global, tt.text)
			wantRefused(t, rs, tt.rule, tt.value)
		})
	}
}

// TestYAMLv4Loads covers the inputs that load under v4 and that v3 read
// otherwise.
func TestYAMLv4Loads(t *testing.T) {
	// v3 refused a lone document end marker as a syntax error. v4
	// reads it as a stream with no document, like an empty file: an
	// empty policy, which allows nothing.
	f := mustLoad(t, repo, "...\n")
	if len(f.Endpoints)+len(f.PassHosts)+len(f.RawTCP)+len(f.PartsOff) != 0 || f.WildcardBudget != nil {
		t.Errorf("a file with no document loaded as %+v, want an empty policy", f)
	}
	// A %TAG directive that names the core schema's prefix gives a
	// core tag, in v3 and v4 alike.
	mustLoad(t, repo, "%TAG !e! tag:yaml.org,2002:\n---\nversion: !e!int 1\n")
	// YAML 1.1 booleans are strings in v3 and v4 alike.
	f = mustLoad(t, repo, strings.Replace(withRule("{host: example.com, port: 443}"), "  r:\n", "  r:\n    name: yes\n", 1))
	if len(f.Endpoints) != 1 || f.Endpoints[0].RuleName != "yes" {
		t.Errorf("endpoints = %+v, want one of rule yes", f.Endpoints)
	}
}

// TestFormRefusesNodeKinds checks that walker refuses a stream node, and
// a kind no version has yet, with RuleYAMLForm and the kind's name. The
// loader doesn't make stream nodes (newLoader), so the tree is built by
// hand.
func TestFormRefusesNodeKinds(t *testing.T) {
	tests := []struct {
		kind yaml.Kind
		name string
	}{
		{yaml.StreamNode, "a stream node"},
		{yaml.Kind(1 << 20), "node kind 1048576"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCollector(testFile)
			doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: tt.kind, Line: 3}}}
			w := walker{c: c}
			w.walk(doc, 0)
			r := wantRefused(t, c.err(), RuleYAMLForm, "")
			if r.Line != 3 || !strings.Contains(r.Error(), tt.name) {
				t.Errorf("refusal %q at line %d, want line 3 naming %s", r.Error(), r.Line, tt.name)
			}
		})
	}
}

// TestKindName checks that kindName names each kind a refusal can show.
func TestKindName(t *testing.T) {
	tests := []struct {
		n    *yaml.Node
		want string
	}{
		{&yaml.Node{Kind: yaml.MappingNode}, "a mapping"},
		{&yaml.Node{Kind: yaml.SequenceNode}, "a list"},
		{&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int"}, "a scalar of type !!int"},
		{&yaml.Node{Kind: yaml.DocumentNode}, "a document node"},
		{&yaml.Node{Kind: yaml.AliasNode}, "an alias"},
		{&yaml.Node{Kind: yaml.StreamNode}, "a stream node"},
		{&yaml.Node{Kind: yaml.Kind(1 << 20)}, "node kind 1048576"},
	}
	for _, tt := range tests {
		if got := kindName(tt.n); got != tt.want {
			t.Errorf("kindName(%d) = %q, want %q", tt.n.Kind, got, tt.want)
		}
	}
}
