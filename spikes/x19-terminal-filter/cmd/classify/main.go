// Command classify lists every escape sequence and control in the output
// of one or more recordings, with counts, an example, and the filter's
// decision.
//
//	classify recordings/*.cast
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"

	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/castio"
	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/filter"
)

type row struct {
	count   int
	files   map[string]bool
	example string
	verdict string
}

func main() {
	flag.Parse()
	rows := map[string]*row{}
	var total, textBytes int
	for _, path := range flag.Args() {
		_, evs, err := castio.Read(path)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		var tok filter.Tokenizer
		out := castio.Output(evs)
		total += len(out)
		tok.Feed(out, func(t filter.Token) {
			if t.Kind == filter.Text {
				textBytes += len(t.Raw)
				return
			}
			k := filter.Key(t)
			r := rows[k]
			if r == nil {
				ex := t.Raw
				if len(ex) > 80 {
					ex = ex[:80]
				}
				d := filter.Decide(t)
				v := "pass"
				if !d.Pass {
					v = "DROP"
				}
				if d.Rewrite != nil {
					v = "rewrite"
				}
				r = &row{files: map[string]bool{}, example: filter.Printable(ex), verdict: v + " (" + d.Rule + ")"}
				rows[k] = r
			}
			r.count++
			r.files[path] = true
		})
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("%d bytes of output, %d bytes of text\n\n", total, textBytes)
	fmt.Println("| Sequence | Count | Recordings | Decision | Example |")
	fmt.Println("|---|---|---|---|---|")
	for _, k := range keys {
		r := rows[k]
		fmt.Printf("| `%s` | %d | %d | %s | `%s` |\n", k, r.count, len(r.files), r.verdict, r.example)
	}
}
