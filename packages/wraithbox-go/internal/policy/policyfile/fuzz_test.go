package policyfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy"
)

// FuzzLoadPolicy loads arbitrary bytes as each source's file. Load must
// not panic, and a file it accepts must hold only what its source may
// hold: valid strings, and from a repository no extension key and no
// audit endpoint (SEC09-host-policy).
func FuzzLoadPolicy(f *testing.F) {
	seeds := []string{
		"",
		"version: 1\n",
		withRule("{host: example.com, port: 443}"),
		withRule("{host: api.anthropic.com, port: 443, protocol: rest, rules: [{allow: {method: POST, path: /v1/oauth/token}}]}"),
		withRule("{host: example.com, port: 443, enforcement: audit}"),
		withRuleAndExt("  pass:\n    - {host: a.example.com, reason: r}\n", "{host: example.com, port: 443}"),
		"a: &a [1]\nb: *a\n",
		"version: 1\n---\n",
	}
	data, err := os.ReadFile(filepath.Join("testdata", "openshell-multi-agent-notepad.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	seeds = append(seeds, string(data))
	for _, s := range seeds {
		for k := range 3 {
			f.Add([]byte(s), uint8(k))
		}
	}
	srcs := []policy.Source{global, project, repo}
	f.Fuzz(func(t *testing.T, data []byte, k uint8) {
		src := srcs[int(k)%len(srcs)]
		file, err := Load(context.Background(), testFile, data, src)
		if err != nil {
			if _, ok := errors.AsType[*Refusals](err); !ok {
				t.Fatalf("error %T, want *Refusals", err)
			}
			return
		}
		checkAccepted(t, file, src)
		checkAcceptedTree(t, data, src)
	})
}

// checkAccepted checks what an accepted file holds.
func checkAccepted(t *testing.T, file *File, src policy.Source) {
	t.Helper()
	if r := file.policy().Check(); r != nil {
		t.Fatalf("accepted file fails the model's check: %v", r)
	}
	if src.Kind != policy.SourceRepository {
		return
	}
	if len(file.PassHosts)+len(file.RawTCP)+len(file.PartsOff) > 0 || file.WildcardBudget != nil {
		t.Fatalf("repository file accepted with extension entries: %+v", file)
	}
	for _, e := range file.Endpoints {
		if e.Enforcement == policy.EnforcementAudit {
			t.Fatalf("repository file accepted with audit on %s", e.Host)
		}
	}
}

// implementedKeys is every key a level may hold, from every level, for
// checkAcceptedTree.
var implementedKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range []level{
		topLevel, ruleLevel, binaryLevel, endpointLevel, allowWrapLevel, httpRuleLevel,
		extensionLevel, passLevel, rawTCPLevel, partOffLevel,
	} {
		for _, k := range l.implemented {
			m[k] = true
		}
	}
	return m
}()

// checkAcceptedTree decodes an accepted file's bytes again and checks,
// apart from the loader's own walk, that no key is outside the
// implemented keys (a rule's key under network_policies excepted), that
// every scalar is printable ASCII, and that a repository file has no
// `wraithbox` key.
func checkAcceptedTree(t *testing.T, data []byte, src policy.Source) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("accepted file doesn't decode: %v", err)
	}
	if len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if src.Kind == policy.SourceRepository && lookup(root, extensionKey) != nil {
		t.Fatalf("repository file accepted with the %s key", extensionKey)
	}
	if s, ok := nonPrintableScalar(root); ok {
		t.Fatalf("accepted file holds the scalar %q", s)
	}
	if k, ok := unknownTopKey(root); ok {
		t.Fatalf("accepted file holds the key %q", k)
	}
}

// nonPrintableScalar returns the first scalar under n, keys included,
// that isn't printable ASCII.
func nonPrintableScalar(n *yaml.Node) (string, bool) {
	if n.Kind == yaml.ScalarNode && !printableASCII(n.Value) {
		return n.Value, true
	}
	for _, c := range n.Content {
		if s, ok := nonPrintableScalar(c); ok {
			return s, true
		}
	}
	return "", false
}

// unknownTopKey returns the first key of the file that no level
// implements. The keys of network_policies are rule names, so only the
// rules under them are checked.
func unknownTopKey(root *yaml.Node) (string, bool) {
	for k, v := range pairs(root) {
		if !implementedKeys[k.Value] {
			return k.Value, true
		}
		if k.Value != "network_policies" {
			if key, ok := unknownKey(v); ok {
				return key, true
			}
			continue
		}
		for _, rule := range pairs(v) {
			if key, ok := unknownKey(rule); ok {
				return key, true
			}
		}
	}
	return "", false
}

// unknownKey returns the first key under n that no level implements.
func unknownKey(n *yaml.Node) (string, bool) {
	for k := range pairs(n) {
		if !implementedKeys[k.Value] {
			return k.Value, true
		}
	}
	for _, c := range n.Content {
		if key, ok := unknownKey(c); ok {
			return key, true
		}
	}
	return "", false
}

func printableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
