package filter

import (
	"bytes"
	"fmt"
	"strings"
)

// CSIParts splits a CSI token into its private-marker prefix (one of
// "<=>?" or ""), parameter string, intermediates and final byte.
func CSIParts(raw []byte) (prefix, params, inter string, final byte) {
	body := raw[2 : len(raw)-1]
	final = raw[len(raw)-1]
	if len(body) > 0 && body[0] >= '<' && body[0] <= '?' {
		prefix, body = string(body[:1]), body[1:]
	}
	j := len(body)
	for j > 0 && body[j-1] >= 0x20 && body[j-1] <= 0x2f {
		j--
	}
	return prefix, string(body[:j]), string(body[j:]), final
}

// OSCParts splits an OSC token into its number and payload.
func OSCParts(tok Token) (num, payload string) {
	body := tok.Raw[2 : len(tok.Raw)-len(tok.Term)]
	n, p, _ := bytes.Cut(body, []byte(";"))
	return string(n), string(p)
}

// Key names a token for the inventory: one key per sequence type, with
// mode numbers kept because they mean different things.
func Key(tok Token) string {
	switch tok.Kind {
	case Text:
		return "text"
	case Control:
		if len(tok.Raw) == 1 {
			return fmt.Sprintf("C0 0x%02x %s", tok.Raw[0], c0Name(tok.Raw[0]))
		}
		return fmt.Sprintf("C1 % x", tok.Raw)
	case Invalid:
		return "invalid UTF-8"
	case Esc:
		return "ESC " + printable(tok.Raw[1:])
	case CSI:
		prefix, params, inter, final := CSIParts(tok.Raw)
		switch {
		case final == 'm' && prefix == "" && inter == "":
			return "CSI m (SGR)"
		case final == 'h' || final == 'l' || (final == 'p' && inter == "$") || final == 'u' || (final == 'm' && prefix == ">") || final == 't' || final == 'n' || final == 'q':
			return fmt.Sprintf("CSI %s%s%s%c", prefix, params, inter, final)
		default:
			return fmt.Sprintf("CSI %s%s%c", prefix, inter, final)
		}
	case OSC:
		num, payload := OSCParts(tok)
		if num == "9" && strings.HasPrefix(payload, "4;") {
			return "OSC 9;4 (progress)"
		}
		return "OSC " + num
	case DCS:
		if len(tok.Raw) > 3 {
			return "DCS " + printable(tok.Raw[2:min(len(tok.Raw), 4)])
		}
		return "DCS"
	case Aborted:
		return "aborted " + printable(tok.Raw[:min(len(tok.Raw), 3)])
	default:
		return tok.Kind.String()
	}
}

func printable(p []byte) string {
	var b strings.Builder
	for _, c := range p {
		if c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&b, "\\x%02x", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Printable renders raw bytes with escapes visible, for logs.
func Printable(p []byte) string { return printable(p) }

func c0Name(b byte) string {
	names := map[byte]string{
		0x00: "NUL", 0x05: "ENQ", 0x07: "BEL", 0x08: "BS", 0x09: "HT", 0x0a: "LF",
		0x0b: "VT", 0x0c: "FF", 0x0d: "CR", 0x0e: "SO", 0x0f: "SI", 0x7f: "DEL",
	}
	if n, ok := names[b]; ok {
		return n
	}
	return ""
}
