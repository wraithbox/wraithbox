package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// schema is the closed key set as a tree. A table maps each of its keys
// to the key's schema. A key that holds a value maps to nil.
type schema map[string]schema

// schemaOf builds the schema of a decode target from its toml tags. A
// field without a tag has no key, so a key for it is refused.
func schemaOf(t reflect.Type) schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	s := schema{}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		s[name] = schemaOf(f.Type)
	}
	return s
}

// keyCheck walks the document's keys before the decoder sees it. The
// decoder matches a key to a field without regard to case, so
// VM_CPU_COUNT would set vm_cpu_count, and a second spelling of a key
// would replace the first. Here every key must equal a tag exactly
// (S17-05-upgrades-and-migrations, "Configuration"). It also refuses a
// key or table defined twice.
type keyCheck struct {
	p       *unstable.Parser
	file    string
	root    schema
	renamed map[string]string
	defined map[string]bool
	// table is the path of the last table header, and inArrayTable says
	// it was an array table, under which keys repeat.
	table        []string
	inArrayTable bool
}

// checkKeys refuses the first key in file order that isn't in root,
// and the first key or table defined twice. A document the parser can't
// read is left to the decoder, which reports the line.
func checkKeys(file string, data []byte, root schema, renamed map[string]string) *Refusal {
	p := &unstable.Parser{}
	p.Reset(data)
	c := keyCheck{p: p, file: file, root: root, renamed: renamed, defined: map[string]bool{}}
	for p.NextExpression() {
		if r := c.expression(p.Expression()); r != nil {
			return r
		}
	}
	return nil
}

// expression checks one top-level expression: a table header, or a key
// and its value. Comments have no key.
func (c *keyCheck) expression(e *unstable.Node) *Refusal {
	if e.Kind == unstable.KeyValue {
		return c.keyValue(c.table, e, !c.inArrayTable)
	}
	if e.Kind != unstable.Table && e.Kind != unstable.ArrayTable {
		return nil
	}
	c.table = nil
	c.inArrayTable = e.Kind == unstable.ArrayTable
	if r := c.key(&c.table, e.Key()); r != nil {
		return r
	}
	if c.inArrayTable {
		return nil
	}
	return c.define(c.table, firstKey(e))
}

// keyValue checks a key and, inside an inline table or an array, the
// keys of its value. track is false where a key may repeat: under an
// array table and inside an array.
func (c *keyCheck) keyValue(prefix []string, kv *unstable.Node, track bool) *Refusal {
	path := append([]string(nil), prefix...)
	if r := c.key(&path, kv.Key()); r != nil {
		return r
	}
	if track {
		if r := c.define(path, firstKey(kv)); r != nil {
			return r
		}
	}
	return c.value(path, kv.Value(), track)
}

// value checks the keys inside an inline table, and inside each element
// of an array. Any other value has no key.
func (c *keyCheck) value(path []string, v *unstable.Node, track bool) *Refusal {
	if v.Kind != unstable.InlineTable && v.Kind != unstable.Array {
		return nil
	}
	for it := v.Children(); it.Next(); {
		var r *Refusal
		if v.Kind == unstable.InlineTable {
			r = c.keyValue(path, it.Node(), track)
		} else {
			r = c.value(path, it.Node(), false)
		}
		if r != nil {
			return r
		}
	}
	return nil
}

// key appends the parts of a dotted key to path, and checks each against
// the schema. Below a key that holds a value, the decoder refuses the
// type, so the check stops there.
func (c *keyCheck) key(path *[]string, it unstable.Iterator) *Refusal {
	for it.Next() {
		*path = append(*path, string(it.Node().Data))
		node, known := c.lookup(*path)
		if !known {
			return c.unknown(*path, it.Node())
		}
		if node == nil {
			return nil
		}
	}
	return nil
}

// lookup returns the schema at path and whether every part is known. A
// path through a key that holds a value is known.
func (c *keyCheck) lookup(path []string) (schema, bool) {
	node := c.root
	for _, part := range path {
		if node == nil {
			return nil, true
		}
		child, ok := node[part]
		if !ok {
			return nil, false
		}
		node = child
	}
	return node, true
}

// firstKey returns the first part of an expression's key.
func firstKey(n *unstable.Node) *unstable.Node {
	it := n.Key()
	return it.Node()
}

func (c *keyCheck) line(n *unstable.Node) int {
	return c.p.Shape(n.Raw).Start.Line
}

func (c *keyCheck) unknown(path []string, n *unstable.Node) *Refusal {
	key := strings.Join(path, ".")
	line := c.line(n)
	if newKey, ok := c.renamed[key]; ok {
		return &Refusal{
			Rule: RuleKeyRenamed, File: c.file, Line: line, Key: key, NewKey: newKey,
			msg: fmt.Sprintf("the settings file %s has the key %s at line %d, which a release renamed or removed: use %s",
				c.file, key, line, newKey),
		}
	}
	return &Refusal{
		Rule: RuleKeyUnknown, File: c.file, Line: line, Key: key,
		msg: fmt.Sprintf("the settings file %s has the unknown key %s at line %d. Keys match exactly, and are lowercase", c.file, key, line),
	}
}

// define records path as defined, and refuses a second definition. The
// parts are quoted, so a quoted key with a dot in it can't stand for a
// dotted key.
func (c *keyCheck) define(path []string, first *unstable.Node) *Refusal {
	quoted := make([]string, len(path))
	for i, p := range path {
		quoted[i] = strconv.Quote(p)
	}
	id := strings.Join(quoted, ".")
	if !c.defined[id] {
		c.defined[id] = true
		return nil
	}
	key := strings.Join(path, ".")
	line := c.line(first)
	return &Refusal{
		Rule: RuleSyntaxInvalid, File: c.file, Line: line, Key: key,
		msg: fmt.Sprintf("the settings file %s defines %s a second time at line %d", c.file, key, line),
	}
}
