// Package plist decodes an XML property list into Go values. It reads
// the output of macOS tools such as diskutil's -plist form, and bundle
// Info.plist files. A binary property list is refused.
//
// The values are map[string]any for a dict, []any for an array, string
// for a string or a date, int64 for a decimal integer, float64 for a
// real, bool for true and false, and []byte for data. A dict with a key
// twice, an element out of place, a second value in <plist>, and nesting
// deeper than MaxDepth are errors.
package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxDepth bounds the nesting of dicts and arrays.
const MaxDepth = 32

// ErrBinary is the error for a binary property list.
var ErrBinary = errors.New("plist: a binary property list")

// Decode decodes the one value in the plist element of data.
func Decode(data []byte) (any, error) {
	if bytes.HasPrefix(data, []byte("bplist")) {
		return nil, ErrBinary
	}
	d := &decoder{x: xml.NewDecoder(bytes.NewReader(data))}
	// A property list declares no entity, so none is resolved.
	d.x.Strict = true
	start, err := d.nextStart()
	if err != nil {
		return nil, err
	}
	if start.Name.Local != "plist" {
		return nil, fmt.Errorf("plist: the root element is <%s>, not <plist>", start.Name.Local)
	}
	inner, err := d.nextStart()
	if err != nil {
		return nil, err
	}
	v, err := d.value(inner, 1)
	if err != nil {
		return nil, err
	}
	if err := d.end(); err != nil {
		return nil, err
	}
	return v, nil
}

// end requires that </plist> follows the one value, and that only
// white space, comments and processing instructions follow it.
func (d *decoder) end() error {
	start, ok, err := d.startOrEnd()
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("plist: <%s> after the value", start.Name.Local)
	}
	for {
		tok, err := d.x.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return fmt.Errorf("plist: <%s> after </plist>", t.Name.Local)
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return errors.New("plist: text after </plist>")
			}
		}
	}
}

type decoder struct {
	x *xml.Decoder
}

// nextStart returns the next start element, skipping text, comments,
// processing instructions and the doctype. An end element first is an
// error.
func (d *decoder) nextStart() (xml.StartElement, error) {
	for {
		tok, err := d.x.Token()
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.EndElement:
			return xml.StartElement{}, fmt.Errorf("plist: unexpected </%s>", t.Name.Local)
		}
	}
}

// startOrEnd returns the next start element and true, or false at the
// end element of the container.
func (d *decoder) startOrEnd() (xml.StartElement, bool, error) {
	for {
		tok, err := d.x.Token()
		if err != nil {
			return xml.StartElement{}, false, fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, true, nil
		case xml.EndElement:
			return xml.StartElement{}, false, nil
		}
	}
}

// value decodes the element that start opens.
func (d *decoder) value(start xml.StartElement, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("plist: nested deeper than %d", MaxDepth)
	}
	switch start.Name.Local {
	case "dict":
		return d.dict(depth)
	case "array":
		return d.array(depth)
	case "true", "false":
		if err := d.x.Skip(); err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		return start.Name.Local == "true", nil
	case "string", "date":
		return d.text()
	case "integer":
		return d.integer()
	case "real":
		return d.real()
	case "data":
		return d.data()
	}
	return nil, fmt.Errorf("plist: unknown element <%s>", start.Name.Local)
}

func (d *decoder) dict(depth int) (map[string]any, error) {
	out := map[string]any{}
	for {
		start, ok, err := d.startOrEnd()
		if err != nil || !ok {
			return out, err
		}
		if start.Name.Local != "key" {
			return nil, fmt.Errorf("plist: <%s> where a dict has a <key>", start.Name.Local)
		}
		key, err := d.text()
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("plist: the key %q is in a dict twice", key)
		}
		vstart, err := d.nextStart()
		if err != nil {
			return nil, err
		}
		if out[key], err = d.value(vstart, depth+1); err != nil {
			return nil, err
		}
	}
}

func (d *decoder) array(depth int) ([]any, error) {
	out := []any{}
	for {
		start, ok, err := d.startOrEnd()
		if err != nil || !ok {
			return out, err
		}
		v, err := d.value(start, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// text reads the character data up to the element's end. A child
// element is an error.
func (d *decoder) text() (string, error) {
	var b strings.Builder
	for {
		tok, err := d.x.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return "", fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		case xml.StartElement:
			return "", fmt.Errorf("plist: <%s> inside a text element", t.Name.Local)
		}
	}
}

func (d *decoder) integer() (int64, error) {
	s, err := d.text()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("plist: integer %q: %w", s, err)
	}
	return v, nil
}

func (d *decoder) real() (float64, error) {
	s, err := d.text()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("plist: real %q: %w", s, err)
	}
	return v, nil
}

func (d *decoder) data() ([]byte, error) {
	s, err := d.text()
	if err != nil {
		return nil, err
	}
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	v, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("plist: data: %w", err)
	}
	return v, nil
}
