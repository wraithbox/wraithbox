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
// field without a tag has no key, so a key for it is refused. A slice
// or array has the schema of its element, so the keys of an array of
// tables are checked as a table's are.
//
// A map's keys are data, not setting names: the decoder keeps them as
// written, case included, and the code that reads the map decides what
// each means. So a map has no schema here, as a value doesn't.
func schemaOf(t reflect.Type) schema {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
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

// definition is how a path was defined: as a key or table, or as an
// array of tables, which a later [[header]] of the same path extends.
type definition int

const (
	definedOnce definition = iota + 1
	definedArray
)

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
	defined map[string]definition
	// table is the path of the last table header.
	table []string
}

// checkKeys refuses the first key in file order that isn't in root,
// and the first key or table defined twice. A document the parser can't
// read is left to the decoder, which reports the line.
func checkKeys(file string, data []byte, root schema, renamed map[string]string) *Refusal {
	p := &unstable.Parser{}
	p.Reset(data)
	c := keyCheck{p: p, file: file, root: root, renamed: renamed, defined: map[string]definition{}}
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
		return c.keyValue(c.table, e)
	}
	if e.Kind != unstable.Table && e.Kind != unstable.ArrayTable {
		return nil
	}
	c.table = nil
	if r := c.key(&c.table, e.Key()); r != nil {
		return r
	}
	if e.Kind == unstable.ArrayTable {
		return c.newElement(c.table, firstKey(e))
	}
	return c.define(c.table, firstKey(e))
}

// keyValue checks a key and the keys inside its value.
func (c *keyCheck) keyValue(prefix []string, kv *unstable.Node) *Refusal {
	path := append([]string(nil), prefix...)
	if r := c.key(&path, kv.Key()); r != nil {
		return r
	}
	if r := c.define(path, firstKey(kv)); r != nil {
		return r
	}
	return c.value(path, kv.Value())
}

// value checks the keys inside an inline table, and inside each element
// of an array. Each element is a table of its own, so its keys are
// tracked apart from the other elements'. Any other value has no key.
func (c *keyCheck) value(path []string, v *unstable.Node) *Refusal {
	if v.Kind != unstable.InlineTable && v.Kind != unstable.Array {
		return nil
	}
	for it := v.Children(); it.Next(); {
		var r *Refusal
		if v.Kind == unstable.InlineTable {
			r = c.keyValue(path, it.Node())
		} else {
			element := *c
			element.defined = map[string]definition{}
			r = element.value(path, it.Node())
		}
		if r != nil {
			return r
		}
	}
	return nil
}

// key appends every part of a dotted key to path, then checks each
// prefix against the schema. Below a key that holds a value, the
// decoder refuses the type, so the check stops there.
func (c *keyCheck) key(path *[]string, it unstable.Iterator) *Refusal {
	base := len(*path)
	var parts []*unstable.Node
	for it.Next() {
		*path = append(*path, string(it.Node().Data))
		parts = append(parts, it.Node())
	}
	for i, part := range parts {
		node, known := c.lookup((*path)[:base+i+1])
		if !known {
			return c.unknown(*path, base+i, part)
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

// unknown refuses path, whose part at index first is the first one the
// schema doesn't have. The renamed table is asked for the whole dotted
// key, then for each shorter prefix down to that part.
func (c *keyCheck) unknown(path []string, first int, n *unstable.Node) *Refusal {
	candidates := make([]string, 0, len(path)-first)
	for end := len(path); end > first; end-- {
		candidates = append(candidates, strings.Join(path[:end], "."))
	}
	return refuseKey(c.file, c.line(n), candidates, c.renamed)
}

// refuseKey builds the refusal of an unknown key. candidates[0] is the
// whole dotted key. The first candidate in the renamed table gives
// config-key-renamed with its new key, and none gives
// config-key-unknown.
func refuseKey(file string, line int, candidates []string, renamed map[string]string) *Refusal {
	for _, key := range candidates {
		if newKey, ok := renamed[key]; ok {
			return &Refusal{
				Rule: RuleKeyRenamed, File: file, Line: line, Key: key, NewKey: newKey,
				msg: fmt.Sprintf("the settings file %s has the key %s at line %d, which a release renamed or removed: use %s",
					file, key, line, newKey),
			}
		}
	}
	key := candidates[0]
	return &Refusal{
		Rule: RuleKeyUnknown, File: file, Line: line, Key: key,
		msg: fmt.Sprintf("the settings file %s has the unknown key %s at line %d. Keys match exactly, and are lowercase", file, key, line),
	}
}

// id is the identity of a path in the defined set. The parts are
// quoted, so a quoted key with a dot in it can't stand for a dotted key.
func id(path []string) string {
	quoted := make([]string, len(path))
	for i, p := range path {
		quoted[i] = strconv.Quote(p)
	}
	return strings.Join(quoted, ".")
}

// define records path as defined, and refuses a second definition.
func (c *keyCheck) define(path []string, first *unstable.Node) *Refusal {
	k := id(path)
	if _, ok := c.defined[k]; ok {
		return c.twice(path, first)
	}
	c.defined[k] = definedOnce
	return nil
}

// newElement starts a new element of the array of tables at path. Its
// keys and sub-tables are new, so what the previous element defined is
// forgotten. A path defined as a key or a table first is refused.
func (c *keyCheck) newElement(path []string, first *unstable.Node) *Refusal {
	k := id(path)
	if d, ok := c.defined[k]; ok && d != definedArray {
		return c.twice(path, first)
	}
	c.defined[k] = definedArray
	for other := range c.defined {
		if strings.HasPrefix(other, k+".") {
			delete(c.defined, other)
		}
	}
	return nil
}

func (c *keyCheck) twice(path []string, first *unstable.Node) *Refusal {
	key := strings.Join(path, ".")
	line := c.line(first)
	return &Refusal{
		Rule: RuleSyntaxInvalid, File: c.file, Line: line, Key: key,
		msg: fmt.Sprintf("the settings file %s defines %s a second time at line %d", c.file, key, line),
	}
}
