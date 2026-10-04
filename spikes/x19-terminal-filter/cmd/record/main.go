// Command record runs a program in a PTY behind a headless terminal
// emulator, drives it from a script, and writes every byte the program
// prints to an asciicast v2 file. The emulator answers the program's
// terminal queries the way the chosen profile's terminal would.
//
//	record -o out.cast -profile ghostty -script s.txt -- claude --model haiku
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/wraithbox/wraithbox/spikes/x19-terminal-filter/filter"
)

type profile struct {
	env     map[string]string
	da1     string
	modes   map[int]int // DECRQM answers: 1 set, 2 reset; missing = no reply
	xtver   string      // XTVERSION reply payload, "" = no reply
	kitty   bool        // answers CSI ? u
	bg      string      // OSC 11 reply, "" = no reply
	osc10fg string
}

var profiles = map[string]profile{
	"ghostty": {
		env: map[string]string{
			"TERM": "xterm-ghostty", "TERM_PROGRAM": "ghostty", "TERM_PROGRAM_VERSION": "1.2.3",
			"TERMINFO": "/Applications/Ghostty.app/Contents/Resources/terminfo", "COLORTERM": "truecolor",
		},
		da1:   "\x1b[?62;22;52c",
		modes: map[int]int{2026: 2, 2027: 2, 2004: 2, 1004: 2, 1049: 2, 2048: 2, 2031: 2, 1000: 2, 1006: 2, 25: 1},
		xtver: "ghostty 1.2.3", kitty: true,
		bg: "rgb:2828/2c2c/3434", osc10fg: "rgb:ffff/ffff/ffff",
	},
	"apple": {
		env: map[string]string{
			"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "455.1",
		},
		da1: "\x1b[?1;2c",
		// Terminal.app does not answer DECRQM, XTVERSION or the kitty query.
		bg: "rgb:ffff/ffff/ffff", osc10fg: "rgb:0000/0000/0000",
	},
	"iterm": {
		env: map[string]string{
			"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.5.14",
			"LC_TERMINAL": "iTerm2", "COLORTERM": "truecolor",
		},
		da1:   "\x1b[?62;4c",
		modes: map[int]int{2026: 2, 2004: 2, 1004: 2},
		xtver: "iTerm2 3.5.14", kitty: true,
		bg: "rgb:0000/0000/0000", osc10fg: "rgb:ffff/ffff/ffff",
	},
	"vscode": {
		env: map[string]string{
			"TERM": "xterm-256color", "TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.105.0", "COLORTERM": "truecolor",
		},
		da1:   "\x1b[?1;2c",
		modes: map[int]int{2026: 2, 2004: 2, 1004: 2},
		xtver: "xterm.js(5.6.0)",
		bg:    "rgb:1e1e/1e1e/1e1e", osc10fg: "rgb:cccc/cccc/cccc",
	},
}

type cast struct {
	mu    sync.Mutex
	w     *bufio.Writer
	start time.Time
}

func (c *cast) event(kind, data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal([]any{time.Since(c.start).Seconds(), kind, data})
	c.w.Write(b)
	c.w.WriteByte('\n')
}

func main() {
	out := flag.String("o", "out.cast", "asciicast output")
	prof := flag.String("profile", "ghostty", "terminal profile: ghostty, apple, iterm, vscode")
	script := flag.String("script", "", "input script")
	cols := flag.Int("cols", 120, "columns")
	rows := flag.Int("rows", 40, "rows")
	screens := flag.String("screens", "", "directory for screen snapshots")
	flag.Parse()
	p, ok := profiles[*prof]
	if !ok {
		log.Fatalf("unknown profile %q", *prof)
	}

	cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
	cmd.Env = env(p)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(*cols), Rows: uint16(*rows)})
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	c := &cast{w: bufio.NewWriter(f), start: time.Now()}
	hdr, _ := json.Marshal(map[string]any{
		"version": 2, "width": *cols, "height": *rows, "timestamp": c.start.Unix(),
		"command": strings.Join(flag.Args(), " "), "env": map[string]string{"TERM": p.env["TERM"], "TERM_PROGRAM": p.env["TERM_PROGRAM"]},
	})
	c.w.Write(append(hdr, '\n'))

	emu := vt.NewEmulator(*cols, *rows)
	var emuMu sync.Mutex
	go io.Copy(io.Discard, emu) // the emulator's own replies are replaced by the profile's

	var lastOut time.Time
	var lastMu sync.Mutex
	done := make(chan struct{})
	reply := func(s string) {
		c.event("i", s)
		ptmx.Write([]byte(s))
	}
	go func() {
		defer close(done)
		var tok filter.Tokenizer
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				c.event("o", string(chunk))
				emuMu.Lock()
				emu.Write(chunk)
				emuMu.Unlock()
				lastMu.Lock()
				lastOut = time.Now()
				lastMu.Unlock()
				tok.Feed(chunk, func(t filter.Token) { answer(p, t, emu, &emuMu, reply) })
			}
			if err != nil {
				return
			}
		}
	}()

	snap := func(name string) {
		if *screens == "" {
			return
		}
		emuMu.Lock()
		s := emu.String()
		emuMu.Unlock()
		os.MkdirAll(*screens, 0o755)
		os.WriteFile(fmt.Sprintf("%s/%s.txt", *screens, name), []byte(s), 0o644)
	}
	screen := func() string {
		emuMu.Lock()
		defer emuMu.Unlock()
		return emu.String()
	}
	idle := func(d time.Duration, max time.Duration) {
		deadline := time.Now().Add(max)
		for time.Now().Before(deadline) {
			lastMu.Lock()
			l := lastOut
			lastMu.Unlock()
			if time.Since(l) >= d {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	if *script != "" {
		runScript(*script, ptmx, c, emu, &emuMu, idle, screen, snap)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	cmd.Wait()
	snap("final")
	c.mu.Lock()
	c.w.Flush()
	c.mu.Unlock()
	f.Close()
}

func env(p profile) []string {
	drop := []string{"CLAUDECODE", "CLAUDE_CODE_", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM", "TERMINFO",
		"ITERM_", "LC_TERMINAL", "TMUX", "STY", "ZELLIJ", "GHOSTTY_", "VSCODE_", "KITTY_", "WEZTERM_", "SSH_", "TERM_SESSION_ID",
		"__CFBundleIdentifier", "AI_AGENT"}
	var out []string
outer:
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		for _, d := range drop {
			if k == d || (strings.HasSuffix(d, "_") && strings.HasPrefix(k, d)) {
				continue outer
			}
		}
		out = append(out, kv)
	}
	for k, v := range p.env {
		out = append(out, k+"="+v)
	}
	if extra := os.Getenv("X19_EXTRA_ENV"); extra != "" {
		out = append(out, strings.Split(extra, ",")...)
	}
	return out
}

var decrqm = regexp.MustCompile(`^\x1b\[\?(\d+)\$p$`)

// answer replies to terminal queries as the profile's terminal would.
func answer(p profile, t filter.Token, emu *vt.Emulator, mu *sync.Mutex, reply func(string)) {
	raw := string(t.Raw)
	switch t.Kind {
	case filter.CSI:
		switch {
		case raw == "\x1b[c" || raw == "\x1b[0c":
			reply(p.da1)
		case raw == "\x1b[>c" || raw == "\x1b[>0c":
			reply("\x1b[>1;10;0c")
		case raw == "\x1b[5n":
			reply("\x1b[0n")
		case raw == "\x1b[6n":
			mu.Lock()
			pos := emu.CursorPosition()
			mu.Unlock()
			reply(fmt.Sprintf("\x1b[%d;%dR", pos.Y+1, pos.X+1))
		case raw == "\x1b[>q" || raw == "\x1b[>0q":
			if p.xtver != "" {
				reply("\x1bP>|" + p.xtver + "\x1b\\")
			}
		case raw == "\x1b[?u":
			if p.kitty {
				reply("\x1b[?0u")
			}
		case decrqm.MatchString(raw):
			m := decrqm.FindStringSubmatch(raw)
			n, _ := strconv.Atoi(m[1])
			if v, ok := p.modes[n]; ok {
				reply(fmt.Sprintf("\x1b[?%d;%d$y", n, v))
			} else if p.modes != nil {
				reply(fmt.Sprintf("\x1b[?%d;0$y", n))
			}
		}
	case filter.OSC:
		num, payload := filter.OSCParts(t)
		if payload == "?" && num == "11" && p.bg != "" {
			reply("\x1b]11;" + p.bg + "\x1b\\")
		}
		if payload == "?" && num == "10" && p.osc10fg != "" {
			reply("\x1b]10;" + p.osc10fg + "\x1b\\")
		}
	}
}

var keys = map[string]string{
	"enter": "\r", "esc": "\x1b", "tab": "\t", "shift-tab": "\x1b[Z", "up": "\x1b[A", "down": "\x1b[B",
	"left": "\x1b[D", "right": "\x1b[C", "ctrl-c": "\x03", "ctrl-d": "\x04", "ctrl-r": "\x12", "ctrl-o": "\x0f",
	"ctrl-l": "\x0c", "backspace": "\x7f", "space": " ", "focus-in": "\x1b[I", "focus-out": "\x1b[O",
	"1": "1", "2": "2", "y": "y", "n": "n",
}

// runScript executes one command per line:
//
//	sleep <sec> | idle <sec> [max] | wait <regex> [max] | type <text> |
//	key <name>... | paste <text> | resize <cols> <rows> | snap <name>
func runScript(path string, ptmx *os.File, c *cast, emu *vt.Emulator, mu *sync.Mutex,
	idle func(time.Duration, time.Duration), screen func() string, snap func(string),
) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	send := func(s string) {
		c.event("i", s)
		ptmx.Write([]byte(s))
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmd, arg, _ := strings.Cut(line, " ")
		log.Printf("script: %s", line)
		switch cmd {
		case "sleep":
			s, _ := strconv.ParseFloat(arg, 64)
			time.Sleep(time.Duration(s * float64(time.Second)))
		case "idle":
			f := strings.Fields(arg)
			s, _ := strconv.ParseFloat(f[0], 64)
			max := 120.0
			if len(f) > 1 {
				max, _ = strconv.ParseFloat(f[1], 64)
			}
			idle(time.Duration(s*float64(time.Second)), time.Duration(max*float64(time.Second)))
		case "wait":
			re, max := arg, 60.0
			if i := strings.LastIndex(arg, " "); i > 0 {
				if m, err := strconv.ParseFloat(arg[i+1:], 64); err == nil {
					re, max = arg[:i], m
				}
			}
			rx := regexp.MustCompile(re)
			deadline := time.Now().Add(time.Duration(max * float64(time.Second)))
			for !rx.MatchString(screen()) {
				if time.Now().After(deadline) {
					log.Printf("script: wait %q timed out; screen:\n%s", re, screen())
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		case "type":
			for _, r := range arg {
				send(string(r))
				time.Sleep(15 * time.Millisecond)
			}
		case "key":
			for _, k := range strings.Fields(arg) {
				s, ok := keys[k]
				if !ok {
					log.Fatalf("unknown key %q", k)
				}
				send(s)
				time.Sleep(150 * time.Millisecond)
			}
		case "paste":
			send("\x1b[200~" + strings.ReplaceAll(arg, `\n`, "\r") + "\x1b[201~")
		case "resize":
			f := strings.Fields(arg)
			w, _ := strconv.Atoi(f[0])
			h, _ := strconv.Atoi(f[1])
			mu.Lock()
			emu.Resize(w, h)
			mu.Unlock()
			pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
			c.event("r", fmt.Sprintf("%dx%d", w, h))
		case "snap":
			snap(arg)
		default:
			log.Fatalf("unknown script command %q", cmd)
		}
	}
}
