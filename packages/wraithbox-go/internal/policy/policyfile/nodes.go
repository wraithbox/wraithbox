package policyfile

import (
	"iter"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// pairs yields the keys and values of a mapping node. parse has
// checked that every key is a string scalar and none is there twice.
func pairs(n *yaml.Node) iter.Seq2[*yaml.Node, *yaml.Node] {
	return func(yield func(*yaml.Node, *yaml.Node) bool) {
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i], n.Content[i+1]) {
				return
			}
		}
	}
}

// lookup returns the value of a key in a mapping node, or nil.
func lookup(n *yaml.Node, key string) *yaml.Node {
	for k, v := range pairs(n) {
		if k.Value == key {
			return v
		}
	}
	return nil
}

// kindName names a node's kind in a refusal.
func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar of type " + n.ShortTag()
	case yaml.DocumentNode, yaml.AliasNode:
	}
	return "node kind " + strconv.Itoa(int(n.Kind))
}

// mappingNode reports whether n is a mapping, and refuses it otherwise.
func (d *decoder) mappingNode(n *yaml.Node, path string) bool {
	if n.Kind == yaml.MappingNode {
		return true
	}
	d.c.add(RuleValue, n.Line, path, "", "is %s, want a mapping", kindName(n))
	return false
}

// sequenceNode reports whether n is a list, and refuses it otherwise.
func (d *decoder) sequenceNode(n *yaml.Node, path string) bool {
	if n.Kind == yaml.SequenceNode {
		return true
	}
	d.c.add(RuleValue, n.Line, path, "", "is %s, want a list", kindName(n))
	return false
}

// str returns the value of a string scalar, and refuses any other node.
func (d *decoder) str(n *yaml.Node, path string) (string, bool) {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str" {
		return n.Value, true
	}
	d.c.add(RuleValue, n.Line, path, scalarValue(n), "is %s, want a string", kindName(n))
	return "", false
}

// checkedStr returns a string scalar that follows S09-01-policy-files,
// "Strings": 1 to maxLen bytes of printable ASCII.
func (d *decoder) checkedStr(n *yaml.Node, path string, maxLen int) (string, bool) {
	s, ok := d.str(n, path)
	if !ok {
		return "", false
	}
	if s == "" || len(s) > maxLen {
		d.c.add(RuleString, n.Line, path, s, "has %d bytes, want 1 to %d", len(s), maxLen)
		return "", false
	}
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c > 0x7e {
			d.c.add(RuleString, n.Line, path, s, "has a byte that isn't printable ASCII at offset %d", i)
			return "", false
		}
	}
	return s, true
}

// uintValue returns the value of an integer scalar written in decimal
// within 0 to maxV. ok is false, with no refusal, for anything else, so
// the caller refuses it with its own rule.
func uintValue(n *yaml.Node, maxV uint64) (uint64, bool) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" {
		return 0, false
	}
	v, err := strconv.ParseUint(n.Value, 10, 64)
	if err != nil || v > maxV {
		return 0, false
	}
	return v, true
}

// scalarValue returns a scalar's value, to show in a refusal, and ""
// for a mapping or a list.
func scalarValue(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}
