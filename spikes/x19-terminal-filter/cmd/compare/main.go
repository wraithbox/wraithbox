// Command compare replays a recording into two headless terminal
// emulators, one fed the raw output and one fed the filtered output, and
// compares their screens (text and style) after every output chunk.
//
//	compare recordings/*.cast
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/vt"

	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/castio"
	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/filter"
)

func main() {
	verbose := flag.Bool("v", false, "print the first screen that differs in more than links")
	control := flag.Bool("control", false, "negative control: also strip SGR from the filtered side, to show the comparison sees style")
	flag.Parse()
	fmt.Println("| Recording | Chunks | Differing chunks | Of which only links | Final screen | Bytes in | Bytes out | Dropped or rewritten |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, path := range flag.Args() {
		h, evs, err := castio.Read(path)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		raw := vt.NewEmulator(h.Width, h.Height)
		fil := vt.NewEmulator(h.Width, h.Height)
		go io.Copy(io.Discard, raw)
		go io.Copy(io.Discard, fil)
		var sink io.Writer = fil
		if *control {
			sink = stripSGR{fil}
		}
		f := filter.New(sink)
		chunks, diffs, linkOnly := 0, 0, 0
		var first string
		for _, e := range evs {
			switch e.Kind {
			case "r":
				var w, hh int
				fmt.Sscanf(e.Data, "%dx%d", &w, &hh)
				raw.Resize(w, hh)
				fil.Resize(w, hh)
			case "o":
				chunks++
				raw.Write([]byte(e.Data))
				f.Write([]byte(e.Data))
				if f.Pending() > 0 {
					continue // a sequence split over chunks; compare on the next one
				}
				a, b := raw.Render(), fil.Render()
				if a != b {
					diffs++
					if stripLinks(a) == stripLinks(b) {
						linkOnly++
					} else if first == "" {
						first = fmt.Sprintf("chunk %d at %.2fs:\n%s", chunks, e.T, lineDiff(a, b))
					}
				}
			}
		}
		final := "same"
		if ra, fa := raw.Render(), fil.Render(); ra != fa {
			final = "different"
			if stripLinks(ra) == stripLinks(fa) {
				final = "links only"
			}
		}
		var drops []string
		for k, v := range f.Stats.Changed {
			drops = append(drops, fmt.Sprintf("%s=%d", k, v))
		}
		fmt.Printf("| %s | %d | %d | %d | %s | %d | %d | %s |\n", path, chunks, diffs, linkOnly, final, f.Stats.In, f.Stats.Out, strings.Join(drops, ", "))
		if *verbose && first != "" {
			fmt.Println(first)
		}
	}
}

func lineDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	n := 0
	for i := 0; i < max(len(al), len(bl)) && n < 5; i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			n++
			fmt.Fprintf(&out, "  line %d raw: %q\n  line %d fil: %q\n", i, x, i, y)
		}
	}
	return out.String()
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

type stripSGR struct{ w io.Writer }

func (s stripSGR) Write(p []byte) (int, error) {
	_, err := s.w.Write(sgr.ReplaceAll(p, nil))
	return len(p), err
}

var link = regexp.MustCompile(`\x1b\]8;[^\x07\x1b]*(\x07|\x1b\\)`)

// stripLinks removes hyperlink markup, which the filter drops on purpose
// for file:// links.
func stripLinks(s string) string { return link.ReplaceAllString(s, "") }
