package codesign

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// MaxPlistSize is the largest property list DecodePlist reads. A
// development profile is a few KiB, and one with many devices and
// certificates a few hundred.
const MaxPlistSize = 1 << 20

// maxPlistDepth is how deep DecodePlist nests dicts and arrays. A profile
// nests three deep.
const maxPlistDepth = 32

// DecodePlist decodes an XML property list: a dict is a map[string]any,
// an array a []any, string a string, integer an int64, real a float64,
// true and false a bool, date a time.Time and data a []byte. A binary
// property list, a key that is in a dict twice, an element outside that
// list, a list larger than MaxPlistSize or nested deeper than
// maxPlistDepth, and anything after the top value are errors.
func DecodePlist(data []byte) (any, error) {
	if len(data) > MaxPlistSize {
		return nil, fmt.Errorf("plist: %d bytes, over %d", len(data), MaxPlistSize)
	}
	if bytes.HasPrefix(data, []byte("bplist")) {
		return nil, errors.New("plist: a binary property list")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	root, err := nextStart(d)
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "plist" {
		return nil, fmt.Errorf("plist: the root element is <%s>", root.Name.Local)
	}
	start, err := nextStart(d)
	if err != nil {
		return nil, err
	}
	value, err := decodeValue(d, start, 0)
	if err != nil {
		return nil, err
	}
	if err := expectEnd(d); err != nil {
		return nil, err
	}
	return value, nil
}

// nextStart returns the next start element, skipping text that is only
// white space, comments, the XML declaration and the DOCTYPE.
func nextStart(d *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.EndElement:
			return xml.StartElement{}, fmt.Errorf("plist: </%s> where a value was expected", t.Name.Local)
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return xml.StartElement{}, fmt.Errorf("plist: text %q where a value was expected", string(t))
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
		}
	}
}

// expectEnd reads the rest of the document after the top value: the
// end of <plist>, then nothing but white space and comments.
func expectEnd(d *xml.Decoder) error {
	ended := false
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) && ended {
			return nil
		}
		if err != nil {
			return fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if ended || t.Name.Local != "plist" {
				return fmt.Errorf("plist: </%s> after the top value", t.Name.Local)
			}
			ended = true
		case xml.StartElement:
			return fmt.Errorf("plist: <%s> after the top value", t.Name.Local)
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return fmt.Errorf("plist: text %q after the top value", string(t))
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
		}
	}
}

// decodeValue decodes the value that start opens, through its end
// element.
func decodeValue(d *xml.Decoder, start xml.StartElement, depth int) (any, error) {
	switch start.Name.Local {
	case "dict":
		return decodeDict(d, depth+1)
	case "array":
		return decodeArray(d, depth+1)
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		return start.Name.Local == "true", nil
	}
	var text string
	if err := d.DecodeElement(&text, &start); err != nil {
		return nil, fmt.Errorf("plist: <%s>: %w", start.Name.Local, err)
	}
	return decodeScalar(start.Name.Local, text)
}

// decodeScalar converts the text of a scalar element.
func decodeScalar(name, text string) (any, error) {
	switch name {
	case "string":
		return text, nil
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(text))
	case "data":
		clean := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, text)
		return base64.StdEncoding.DecodeString(clean)
	}
	return nil, fmt.Errorf("plist: <%s> is not a property list element", name)
}

// decodeDict decodes the keys and values of a dict, through </dict>.
func decodeDict(d *xml.Decoder, depth int) (map[string]any, error) {
	if depth > maxPlistDepth {
		return nil, fmt.Errorf("plist: nested deeper than %d", maxPlistDepth)
	}
	out := map[string]any{}
	for {
		start, done, err := nextInContainer(d, "dict")
		if err != nil || done {
			return out, err
		}
		key, err := decodeKey(d, start, out)
		if err != nil {
			return nil, err
		}
		valueStart, done, err := nextInContainer(d, "dict")
		if err == nil && done {
			err = fmt.Errorf("plist: the key %q has no value", key)
		}
		if err != nil {
			return nil, err
		}
		if out[key], err = decodeValue(d, valueStart, depth); err != nil {
			return nil, err
		}
	}
}

// decodeKey decodes the <key> element at start, and refuses another
// element and a key the dict out already has.
func decodeKey(d *xml.Decoder, start xml.StartElement, out map[string]any) (string, error) {
	if start.Name.Local != "key" {
		return "", fmt.Errorf("plist: <%s> where a dict has a <key>", start.Name.Local)
	}
	var key string
	if err := d.DecodeElement(&key, &start); err != nil {
		return "", fmt.Errorf("plist: <key>: %w", err)
	}
	if _, dup := out[key]; dup {
		return "", fmt.Errorf("plist: the key %q is in a dict twice", key)
	}
	return key, nil
}

// decodeArray decodes the values of an array, through </array>.
func decodeArray(d *xml.Decoder, depth int) ([]any, error) {
	if depth > maxPlistDepth {
		return nil, fmt.Errorf("plist: nested deeper than %d", maxPlistDepth)
	}
	out := []any{}
	for {
		start, done, err := nextInContainer(d, "array")
		if err != nil || done {
			return out, err
		}
		value, err := decodeValue(d, start, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
}

// nextInContainer returns the next start element inside the container
// called name, or done at its end element.
func nextInContainer(d *xml.Decoder, name string) (xml.StartElement, bool, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return xml.StartElement{}, false, fmt.Errorf("plist: inside <%s>: %w", name, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, false, nil
		case xml.EndElement:
			// The decoder checks that end elements match their start.
			return xml.StartElement{}, true, nil
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return xml.StartElement{}, false, fmt.Errorf("plist: text %q inside <%s>", string(t), name)
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
		}
	}
}
