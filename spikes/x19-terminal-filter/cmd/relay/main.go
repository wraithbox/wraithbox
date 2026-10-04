// Command relay runs a program in a PTY and relays its output to this
// terminal through the X19 allowlist filter, the way `wb claude` would
// relay a guest's PTY. Input passes through unchanged.
//
//	go run ./cmd/relay -- claude          # filtered
//	go run ./cmd/relay -raw -- claude     # unfiltered, to compare
//	go run ./cmd/relay -log drops.txt -- claude
//
// On exit it prints how many sequences it dropped or rewrote, by rule.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/filter"
)

func main() {
	raw := flag.Bool("raw", false, "do not filter (baseline)")
	logPath := flag.String("log", "", "write each dropped or rewritten sequence to this file")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: relay [-raw] [-log file] -- command [args]")
		os.Exit(2)
	}

	cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Fatal(err)
	}
	defer ptmx.Close()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winch <- syscall.SIGWINCH

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatal(err)
	}
	restore := func() { term.Restore(int(os.Stdin.Fd()), old) }

	go io.Copy(ptmx, os.Stdin)

	var out io.Writer = os.Stdout
	var f *filter.Filter
	if !*raw {
		f = filter.New(os.Stdout)
		if *logPath != "" {
			lf, err := os.Create(*logPath)
			if err != nil {
				restore()
				log.Fatal(err)
			}
			defer lf.Close()
			f.OnDrop = func(t filter.Token, d filter.Decision) {
				ex := t.Raw
				if len(ex) > 200 {
					ex = ex[:200]
				}
				fmt.Fprintf(lf, "%s\t%s\t%s\n", d.Rule, filter.Key(t), filter.Printable(ex))
			}
		}
		out = f
	}
	io.Copy(out, ptmx)
	cmd.Wait()
	restore()

	if f != nil {
		rules := make([]string, 0, len(f.Stats.Changed))
		for r := range f.Stats.Changed {
			rules = append(rules, r)
		}
		sort.Strings(rules)
		fmt.Fprintf(os.Stderr, "x19 relay: %d bytes in, %d out\n", f.Stats.In, f.Stats.Out)
		for _, r := range rules {
			fmt.Fprintf(os.Stderr, "  %-28s %d\n", r, f.Stats.Changed[r])
		}
	}
}
