// Package castio reads the asciicast v2 files that cmd/record writes.
package castio

import (
	"bufio"
	"encoding/json"
	"os"
)

type Header struct {
	Width, Height int
	Command       string
	Env           map[string]string
}

type Event struct {
	T    float64
	Kind string // "o" output, "i" input, "r" resize
	Data string
}

func Read(path string) (Header, []Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var h Header
	var evs []Event
	first := true
	for sc.Scan() {
		if first {
			first = false
			if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
				return h, nil, err
			}
			continue
		}
		var raw []any
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			return h, nil, err
		}
		evs = append(evs, Event{T: raw[0].(float64), Kind: raw[1].(string), Data: raw[2].(string)})
	}
	return h, evs, sc.Err()
}

// Output concatenates the output events.
func Output(evs []Event) []byte {
	var b []byte
	for _, e := range evs {
		if e.Kind == "o" {
			b = append(b, e.Data...)
		}
	}
	return b
}
