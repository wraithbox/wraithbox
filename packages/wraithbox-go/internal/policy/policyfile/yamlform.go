package policyfile

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v4"
)

// The decoder's bounds, those of OpenShell's own parser
// (X24-openshell-artifacts, "Policy schema"; S10-01-libraries,
// "Network policy schema"). A file past one is refused with
// RuleYAMLBound.
const (
	// MaxFileSize is checked before the bytes are parsed.
	MaxFileSize = 4 << 20
	// MaxDepth counts the mappings and sequences on the way from the
	// root to a node, the root included.
	MaxDepth = 64
	// MaxNodes counts every mapping, sequence and scalar, keys
	// included.
	MaxNodes = 100_000
)

// coreTags are the tags of YAML's core schema, the only explicit tags a
// file may use.
var coreTags = map[string]bool{
	"!!str": true, "!!int": true, "!!bool": true, "!!null": true,
	"!!float": true, "!!seq": true, "!!map": true,
}

// newLoader returns a loader for data with each option that a check in
// this file depends on set, so a change of v4's defaults doesn't change
// what loads (I770):
//   - WithV3Defaults keeps the loader limits and the error text of v3.
//   - WithStreamNodes(false) keeps stream nodes out of the tree. walker
//     refuses one all the same.
//   - WithSingleDocument(false) reads past the first document, so parse
//     sees and refuses a second one.
//
// Duplicate keys, merge keys and aliases are refused by walker, which
// doesn't depend on an option.
func newLoader(data []byte) (*yaml.Loader, error) {
	return yaml.NewLoader(bytes.NewReader(data),
		yaml.WithV3Defaults(),
		yaml.WithStreamNodes(false),
		yaml.WithSingleDocument(false),
	)
}

// nonSpecificTag is the tag v4 keeps on a node written with `!` or
// `!<!>`.
const nonSpecificTag = "!"

// parse decodes data into one node tree and checks its bounds and form
// before any value is read. It returns nil for a file with no document,
// and nil with refusals in c for a refused file.
//
// The node tree keeps an alias as a node that points at its anchor, so
// nothing is expanded, and form refuses every alias and anchor.
func parse(c *collector, data []byte) *yaml.Node {
	if len(data) > MaxFileSize {
		c.add(RuleYAMLBound, 0, "", "", "the file has %d bytes, at most %d", len(data), MaxFileSize)
		return nil
	}
	loader, err := newLoader(data)
	if err != nil {
		c.add(RuleYAMLSyntax, 0, "", "", "the decoder can't start: %s", escape(err.Error()))
		return nil
	}
	var doc yaml.Node
	if err := loader.Load(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		c.add(RuleYAMLSyntax, 0, "", "", "the file isn't YAML the decoder reads: %s", escape(err.Error()))
		return nil
	}
	var second yaml.Node
	if err := loader.Load(&second); !errors.Is(err, io.EOF) {
		c.add(RuleYAMLForm, second.Line, "", "", "the file holds a second document, and a policy file has one")
		return nil
	}
	w := walker{c: c}
	w.walk(&doc, 0)
	if c.failed() {
		return nil
	}
	if len(doc.Content) != 1 {
		return nil
	}
	return doc.Content[0]
}

// walker checks the bounds and the form of a node tree. It keeps the key
// path as a stack of the keys and indexes above the node, and formats it
// only for a refusal, so a wide or deep file doesn't make a string for
// each node.
type walker struct {
	c     *collector
	nodes int
	stack []step
}

// step is one level of a key path: a mapping key, or a list index when
// key is nil.
type step struct {
	key   *yaml.Node
	index int
}

// path formats the key path of the node being walked.
func (w *walker) path() string {
	p := ""
	for _, s := range w.stack {
		if s.key != nil {
			p = child(p, s.key.Value)
		} else {
			p = index(p, s.index)
		}
	}
	return p
}

// within walks n with s pushed on the key path.
func (w *walker) within(s step, n *yaml.Node, depth int) {
	w.stack = append(w.stack, s)
	w.walk(n, depth)
	w.stack = w.stack[:len(w.stack)-1]
}

// walk checks one node and its children. depth is the number of
// mappings and sequences above the node, so a mapping or sequence at
// depth MaxDepth-1 is the deepest one that loads.
func (w *walker) walk(n *yaml.Node, depth int) {
	if w.c.failed() {
		return
	}
	if n.Kind != yaml.DocumentNode {
		w.nodes++
		if w.nodes > MaxNodes {
			w.c.add(RuleYAMLBound, n.Line, w.path(), "", "the file has more than %d nodes", MaxNodes)
			return
		}
	}
	if !w.form(n) {
		return
	}
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		depth++
		if depth > MaxDepth {
			w.c.add(RuleYAMLBound, n.Line, w.path(), "", "the file nests deeper than %d", MaxDepth)
			return
		}
		if n.Kind == yaml.MappingNode {
			w.mapping(n, depth)
			return
		}
		for i, e := range n.Content {
			w.within(step{index: i}, e, depth)
		}
	case yaml.DocumentNode:
		for _, e := range n.Content {
			w.walk(e, depth)
		}
	case yaml.ScalarNode, yaml.AliasNode, yaml.StreamNode:
		// A scalar has no children, and form has refused an alias and
		// a stream node.
	}
}

// treeKinds are the node kinds a policy file's tree may hold. Any other
// kind, such as v4's StreamNode or one a later version adds, is refused
// (S09-01-policy-files, "Format").
var treeKinds = map[yaml.Kind]bool{
	yaml.DocumentNode: true, yaml.MappingNode: true, yaml.SequenceNode: true, yaml.ScalarNode: true,
}

// form refuses an anchor, an alias, a node of a kind outside treeKinds,
// and an explicit tag outside the core schema, the non-specific tag `!`
// included. Aliases are how a small file becomes a large tree, and
// policy needs none of these (I489, decision 3).
func (w *walker) form(n *yaml.Node) bool {
	switch {
	case n.Kind == yaml.AliasNode:
		w.c.add(RuleYAMLForm, n.Line, w.path(), n.Value, "the file uses an alias, and policy files may not")
		return false
	case !treeKinds[n.Kind]:
		w.c.add(RuleYAMLForm, n.Line, w.path(), "", "the file holds %s, and policy files may not", kindName(n))
		return false
	case n.Anchor != "":
		w.c.add(RuleYAMLForm, n.Line, w.path(), n.Anchor, "the file sets an anchor, and policy files may not")
		return false
	case n.Style&yaml.TaggedStyle != 0 && !coreTags[n.ShortTag()]:
		w.c.add(RuleYAMLForm, n.Line, w.path(), n.Tag, "the file uses a tag outside YAML's core schema")
		return false
	case n.Tag == nonSpecificTag:
		// v3 dropped the tag and resolved the value as if it had none,
		// so `! 1` was an integer. v4 keeps it and makes every value a
		// string, as YAML 1.2 says. Refusing it keeps a value v3 refused
		// as the wrong type from loading as a string (I770).
		w.c.add(RuleYAMLForm, n.Line, w.path(), n.Tag, "the file uses the non-specific tag !, outside YAML's core schema")
		return false
	}
	return true
}

// mapping checks a mapping's keys: each a string, no merge key, none
// twice. Then it walks the values.
func (w *walker) mapping(n *yaml.Node, depth int) {
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		w.walk(k, depth)
		if w.c.failed() {
			return
		}
		if !w.key(k, seen) {
			return
		}
		w.within(step{key: k}, v, depth)
	}
}

// key refuses a key that isn't a string scalar, a merge key, and a
// duplicate.
func (w *walker) key(k *yaml.Node, seen map[string]bool) bool {
	switch {
	case k.ShortTag() == "!!merge":
		w.c.add(RuleYAMLForm, k.Line, w.path(), k.Value, "the file uses a merge key, and policy files may not")
	case k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str":
		w.c.add(RuleYAMLForm, k.Line, w.path(), k.Value, "a key isn't a string")
	case seen[k.Value]:
		w.c.add(RuleYAMLForm, k.Line, child(w.path(), k.Value), k.Value, "the key is there twice")
	default:
		seen[k.Value] = true
		return true
	}
	return false
}
